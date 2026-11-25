"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Button, IconArrowUpRight, IconRefresh, IconSearch, StateView } from "@/components";
import { ApiError, api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type { ReturnSellerDecision } from "@/lib/gateway-types";
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
import type { GwSellerReturnRequest } from "./sellerApiTypes";
import { useSellerContext } from "./SellerShell";
import { SellerDrawer, statusPillClass } from "./SellerUiPrimitives";
import { SHOP_PICKUP_ADDRESS_REQUIRED_SELLER_MESSAGE, parseUiState } from "./types";
import common from "./SellerCommon.module.css";
import styles from "@/app/(seller)/seller/tra-hang/page.module.css";

type ReturnFilterKey = "all" | "PENDING" | "RETURNING" | "RECEIVED" | "ESCALATED" | "DONE";

const EMPTY_DASH = "—";

export interface SellerReturnsClientProps {
  initialReturns: GwSellerReturnRequest[];
  loadError: string | null;
}

export function SellerReturnsClient({ initialReturns, loadError }: SellerReturnsClientProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const uiState = parseUiState(searchParams.get("state"));
  const initialSelectedParam = searchParams.get("selected");
  const initialStatusParam = (searchParams.get("status") ?? "").trim().toUpperCase();

  const { showToast } = useSellerContext();

  const [returns, setReturns] = useState<GwSellerReturnRequest[]>(initialReturns);
  const [refreshing, setRefreshing] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(loadError);
  const [filter, setFilter] = useState<ReturnFilterKey>(() => {
    if (["PENDING", "RETURNING", "RECEIVED", "ESCALATED", "DONE"].includes(initialStatusParam)) {
      return initialStatusParam as ReturnFilterKey;
    }
    return "all";
  });
  const [query, setQuery] = useState("");
  const [drawerReturnId, setDrawerReturnId] = useState<string | null>(initialSelectedParam);

  // Action form state inside drawer
  const [responseNote, setResponseNote] = useState("");
  const [evidenceUrls, setEvidenceUrls] = useState<string[]>([""]);
  const [disputeReason, setDisputeReason] = useState("");
  const [disputeUrls, setDisputeUrls] = useState<string[]>([""]);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setReturns(initialReturns);
  }, [initialReturns]);

  const reloadReturns = useCallback(async () => {
    setRefreshing(true);
    setErrorMsg(null);
    try {
      const res = await api<{ items?: GwSellerReturnRequest[] }>(
        "/api/seller/returns?page=1&page_size=100"
      );
      setReturns(res.items ?? []);
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách yêu cầu trả hàng lúc này."));
    } finally {
      setRefreshing(false);
    }
  }, []);

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
        (r.order_code ?? "").toLowerCase().includes(q) ||
        (r.reason ?? "").toLowerCase().includes(q) ||
        (r.return_tracking_code ?? "").toLowerCase().includes(q)
      );
    });
  }, [returns, filter, query]);

  const activeReturn = useMemo(
    () => (drawerReturnId ? (returns.find((r) => r.id === drawerReturnId) ?? null) : null),
    [returns, drawerReturnId]
  );

  const openReturnDrawer = (ret: GwSellerReturnRequest) => {
    setDrawerReturnId(ret.id);
    setResponseNote("");
    setEvidenceUrls([""]);
    setDisputeReason("");
    setDisputeUrls([""]);
    setFormError(null);
  };

  const closeDrawer = () => {
    setDrawerReturnId(null);
    setFormError(null);
  };

  const handleRespond = async (ret: GwSellerReturnRequest, decision: ReturnSellerDecision) => {
    if (busy) return;
    const cleanNote = responseNote.trim();
    if (decision === "REJECT" && cleanNote.length < 5) {
      setFormError("Vui lòng nhập lý do từ chối tối thiểu 5 ký tự.");
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
      const res = await api<{ return_request?: GwSellerReturnRequest }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/respond`,
        {
          method: "POST",
          body: {
            decision,
            note: cleanNote,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request! : item)));
      } else {
        await reloadReturns();
      }
      setResponseNote("");
      setEvidenceUrls([""]);
      router.refresh();
      const actionMsg =
        decision === "ACCEPT_REFUND"
          ? "Đã chấp nhận hoàn tiền ngay không cần trả hàng."
          : decision === "ACCEPT_RETURN"
            ? "Đã chấp nhận trả hàng và tạo vận đơn chiều trả."
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

  const handleConfirmReceived = async (ret: GwSellerReturnRequest) => {
    if (busy) return;
    setFormError(null);
    setBusy(true);
    try {
      const res = await api<{ return_request?: GwSellerReturnRequest }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/confirm-received`,
        { method: "POST" }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request! : item)));
      } else {
        await reloadReturns();
      }
      router.refresh();
      showToast(`Đã xác nhận nhận hàng trả cho đơn ${ret.order_code}.`);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể xác nhận đã nhận hàng trả lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const handleDisputeGoods = async (ret: GwSellerReturnRequest) => {
    if (busy) return;
    const reason = disputeReason.trim();
    if (reason.length < 5) {
      setFormError("Vui lòng mô tả lý do hàng trả không đúng (tối thiểu 5 ký tự).");
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
      const res = await api<{ return_request?: GwSellerReturnRequest }>(
        `/api/seller/returns/${encodeURIComponent(ret.id)}/dispute`,
        {
          method: "POST",
          body: {
            reason,
            evidence_urls: urlCheck.urls,
          },
        }
      );
      if (res?.return_request) {
        setReturns((prev) => prev.map((item) => (item.id === ret.id ? res.return_request! : item)));
      } else {
        await reloadReturns();
      }
      setDisputeReason("");
      setDisputeUrls([""]);
      router.refresh();
      showToast(`Đã gửi báo cáo hàng sai cho đơn ${ret.order_code}.`);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể gửi khiếu nại hàng trả lúc này."));
    } finally {
      setBusy(false);
    }
  };

  const countCards: Array<{ key: ReturnFilterKey; label: string; count: number }> = [
    { key: "all", label: "Tất cả", count: counts.all },
    { key: "PENDING", label: "Chờ phản hồi", count: counts.PENDING },
    { key: "RETURNING", label: "Đang gửi trả", count: counts.RETURNING },
    { key: "RECEIVED", label: "Chờ xác nhận nhận", count: counts.RECEIVED },
    { key: "ESCALATED", label: "Tranh chấp sàn", count: counts.ESCALATED },
    { key: "DONE", label: "Hoàn tiền / Đóng", count: counts.DONE },
  ];

  const activeStatusKey = activeReturn
    ? normalizeEnumSuffix(activeReturn.status, "RETURN_STATUS_")
    : "";

  return (
    <div className={common.page}>
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Trả hàng &amp; hoàn tiền</h1>
        </div>
        <div className={common.headerActions}>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={refreshing}
            onClick={() => void reloadReturns()}
          >
            <IconRefresh size={14} />
            <span>{refreshing ? "Đang tải..." : "Làm mới"}</span>
          </Button>
          <Button variant="outline" size="sm" shape="app" href="/seller/don-hang">
            <span>Đơn hàng</span>
            <IconArrowUpRight size={14} />
          </Button>
        </div>
      </header>

      {uiState === "loading" && (
        <StateView
          kind="loading"
          surface="app"
          skeletonVariant="table"
          title="Đang tải danh sách yêu cầu trả hàng..."
          rows={5}
        />
      )}

      {(uiState === "error" || (!uiState && errorMsg)) && (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách trả hàng"
          message={errorMsg ?? "Hệ thống xử lý trả hàng tạm gián đoạn."}
          actionLabel="Thử lại"
          onAction={() => void reloadReturns()}
        />
      )}

      {uiState === "empty" && (
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có yêu cầu trả hàng nào"
          message="Khi người mua gửi yêu cầu trả hàng, hồ sơ sẽ hiển thị tại đây."
          actionLabel="Xem đơn hàng"
          actionHref="/seller/don-hang"
        />
      )}

      {!uiState && !errorMsg && (
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
                  key={`m-${c.key}`}
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
              <span className={common.toolbarMeta}>
                {formatInt(filtered.length)} / {formatInt(returns.length)} yêu cầu
              </span>
            </div>

            {filtered.length === 0 ? (
              <div className={common.inlineEmpty}>
                {returns.length === 0
                  ? "Chưa có yêu cầu trả hàng nào."
                  : "Không có yêu cầu trả hàng nào khớp bộ lọc hiện tại."}
              </div>
            ) : (
              <>
                <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
                  <table className={common.dataTable}>
                    <thead>
                      <tr>
                        <th>Mã đơn &amp; lý do</th>
                        <th className={common.numCol}>Hoàn dự kiến</th>
                        <th>Vận đơn trả</th>
                        <th>Hạn xử lý</th>
                        <th>Trạng thái</th>
                        <th className={common.numCol}>Thao tác</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((ret) => {
                        const meta = getReturnStatusInfo(ret.status);
                        const isSelected = drawerReturnId === ret.id;
                        return (
                          <tr key={ret.id} className={isSelected ? common.rowSelected : undefined}>
                            <td>
                              <button
                                type="button"
                                className={common.rowTriggerBtn}
                                onClick={() => openReturnDrawer(ret)}
                              >
                                {ret.order_code || ret.order_id.slice(0, 8)}
                              </button>
                              <span className={`${common.cellSub} ${styles.reasonEllipsis}`}>
                                {ret.reason}
                              </span>
                            </td>
                            <td className={common.numCol}>
                              <strong>{formatVnd(ret.refund_amount_vnd)}</strong>
                            </td>
                            <td>{ret.return_tracking_code || EMPTY_DASH}</td>
                            <td>
                              {ret.deadline_at ? formatDateTimeVi(ret.deadline_at) : EMPTY_DASH}
                            </td>
                            <td>
                              <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                            </td>
                            <td className={common.numCol}>
                              <div className={common.rowHoverActions}>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  onClick={() => openReturnDrawer(ret)}
                                >
                                  Chi tiết
                                </Button>
                              </div>
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
                      <div key={ret.id} className={common.mobileRow}>
                        <div className={common.mobileRowTop}>
                          <div>
                            <button
                              type="button"
                              className={common.rowTriggerBtn}
                              onClick={() => openReturnDrawer(ret)}
                            >
                              {ret.order_code || ret.order_id.slice(0, 8)}
                            </button>
                            <span className={common.cellSub}>{ret.reason}</span>
                          </div>
                          <strong className="num">{formatVnd(ret.refund_amount_vnd)}</strong>
                        </div>
                        <div className={common.mobileRowMeta}>
                          <span className={statusPillClass(meta.tone)}>{meta.label}</span>
                          <Button
                            variant="outline"
                            size="sm"
                            shape="app"
                            onClick={() => openReturnDrawer(ret)}
                          >
                            Xử lý
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </>
            )}
          </section>
        </>
      )}

      <SellerDrawer
        open={Boolean(activeReturn)}
        onClose={closeDrawer}
        title={activeReturn ? `Yêu cầu trả hàng · ${activeReturn.order_code}` : "Chi tiết trả hàng"}
        subtitle={activeReturn ? getReturnStatusInfo(activeReturn.status).label : undefined}
        footer={
          activeReturn ? (
            <div className={styles.drawerFooterRow}>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                href={`/seller/don-hang/${encodeURIComponent(activeReturn.order_id)}`}
              >
                <span>Mở chi tiết đơn</span>
                <IconArrowUpRight size={14} />
              </Button>
              <Button variant="ghost" size="sm" shape="app" onClick={closeDrawer}>
                Đóng
              </Button>
            </div>
          ) : undefined
        }
      >
        {activeReturn && (
          <div className={styles.detailStack}>
            <div className={styles.infoGrid}>
              <div className={styles.infoItem}>
                <span className={styles.infoKey}>Trạng thái</span>
                <div>
                  <span className={statusPillClass(getReturnStatusInfo(activeReturn.status).tone)}>
                    {getReturnStatusInfo(activeReturn.status).label}
                  </span>
                </div>
              </div>
              <div className={styles.infoItem}>
                <span className={styles.infoKey}>Hoàn dự kiến</span>
                <span className={`${styles.infoVal} num`}>
                  {formatVnd(activeReturn.refund_amount_vnd)}
                </span>
              </div>
              <div className={styles.infoItem}>
                <span className={styles.infoKey}>Hạn xử lý kế tiếp</span>
                <span className={styles.infoVal}>
                  {activeReturn.deadline_at
                    ? formatDateTimeVi(activeReturn.deadline_at)
                    : EMPTY_DASH}
                </span>
              </div>
              <div className={styles.infoItem}>
                <span className={styles.infoKey}>Phí trả hàng</span>
                <span className={styles.infoVal}>
                  {activeReturn.return_fee_vnd > 0
                    ? `${formatVnd(activeReturn.return_fee_vnd)} · `
                    : ""}
                  {getReturnFeeBearerLabel(activeReturn.return_fee_bearer)}
                </span>
              </div>
              <div className={styles.infoItem}>
                <span className={styles.infoKey}>Vận đơn chiều trả</span>
                <span className={styles.infoVal}>
                  {activeReturn.return_tracking_code ? (
                    <>
                      <span>{activeReturn.return_tracking_code}</span>
                      {(() => {
                        const labelHref = buildShippingLabelHref(activeReturn.return_tracking_code);
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
                            </a>
                          </>
                        );
                      })()}
                    </>
                  ) : (
                    EMPTY_DASH
                  )}
                </span>
              </div>
            </div>

            <div className={styles.well}>
              <span className={styles.wellHeading}>Nội dung từ người mua</span>
              <p className={styles.wellText}>{activeReturn.reason}</p>
              {(() => {
                const buyerLinks = (activeReturn.evidence_urls ?? [])
                  .map((u) => parseSafeEvidenceUrl(u))
                  .filter((x): x is NonNullable<typeof x> => x !== null);
                if (buyerLinks.length === 0) return null;
                return (
                  <div className={styles.evidenceList}>
                    <span className={styles.infoKey}>Bằng chứng người mua</span>
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
              {(activeReturn.seller_response || activeReturn.seller_note) && (
                <p className={styles.wellText}>
                  <strong>Phản hồi gian hàng:</strong>{" "}
                  {activeReturn.seller_response || activeReturn.seller_note}
                </p>
              )}
              {(() => {
                const sellerLinks = (activeReturn.seller_evidence_urls ?? [])
                  .map((u) => parseSafeEvidenceUrl(u))
                  .filter((x): x is NonNullable<typeof x> => x !== null);
                if (sellerLinks.length === 0) return null;
                return (
                  <div className={styles.evidenceList}>
                    <span className={styles.infoKey}>Chứng từ gian hàng</span>
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
              {activeReturn.dispute_reason && (
                <p className={styles.wellText}>
                  <strong>Nội dung tranh chấp:</strong> {activeReturn.dispute_reason}
                </p>
              )}
              {activeReturn.admin_note && (
                <p className={styles.wellText}>
                  <strong>Kết luận quản trị viên:</strong> {activeReturn.admin_note}
                </p>
              )}
            </div>

            {/* PENDING: ACCEPT_RETURN, ACCEPT_REFUND, or REJECT */}
            {activeStatusKey === "PENDING" && (
              <div className={styles.well}>
                <span className={styles.wellHeading}>Phản hồi yêu cầu</span>
                <div className={common.field}>
                  <label htmlFor="seller-ret-note" className={common.fieldLabel}>
                    Ghi chú phản hồi (bắt buộc nếu từ chối)
                  </label>
                  <textarea
                    id="seller-ret-note"
                    className={common.textArea}
                    value={responseNote}
                    onChange={(e) => setResponseNote(e.target.value)}
                    placeholder="Nhập hướng dẫn gửi trả hoặc lý do từ chối..."
                    disabled={busy}
                  />
                </div>

                <div className={common.field}>
                  <span className={common.fieldLabel}>
                    Liên kết chứng từ (tối đa {MAX_EVIDENCE_URLS})
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
                        placeholder="https://..."
                        aria-label={`Liên kết chứng từ ${idx + 1}`}
                        disabled={busy}
                      />
                      {evidenceUrls.length > 1 && (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          shape="app"
                          disabled={busy}
                          onClick={() => setEvidenceUrls(evidenceUrls.filter((_, i) => i !== idx))}
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
                    onClick={() => void handleRespond(activeReturn, "REJECT")}
                  >
                    Từ chối
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    shape="app"
                    disabled={busy}
                    onClick={() => void handleRespond(activeReturn, "ACCEPT_REFUND")}
                  >
                    Hoàn tiền ngay
                  </Button>
                  <Button
                    type="button"
                    variant="ink"
                    size="sm"
                    shape="app"
                    disabled={busy}
                    onClick={() => void handleRespond(activeReturn, "ACCEPT_RETURN")}
                  >
                    {busy ? "Đang xử lý..." : "Yêu cầu trả hàng về"}
                  </Button>
                </div>
              </div>
            )}

            {/* RECEIVED: Confirm received or Dispute */}
            {activeStatusKey === "RECEIVED" && (
              <div className={styles.well}>
                <span className={styles.wellHeading}>Kiểm tra hàng trả về kho</span>
                <div className={common.field}>
                  <label htmlFor="seller-dispute-reason" className={common.fieldLabel}>
                    Lý do báo hàng sai (chỉ nhập khi khiếu nại)
                  </label>
                  <textarea
                    id="seller-dispute-reason"
                    className={common.textArea}
                    value={disputeReason}
                    onChange={(e) => setDisputeReason(e.target.value)}
                    placeholder="Mô tả sai khác giữa kiện hàng nhận về và đơn gốc..."
                    disabled={busy}
                  />
                </div>

                <div className={common.field}>
                  <span className={common.fieldLabel}>
                    Bằng chứng mở kiện trả (tối đa {MAX_EVIDENCE_URLS})
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
                        placeholder="https://..."
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
                          onClick={() => setDisputeUrls(disputeUrls.filter((_, i) => i !== idx))}
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
                    onClick={() => void handleDisputeGoods(activeReturn)}
                  >
                    Báo hàng sai
                  </Button>
                  <Button
                    type="button"
                    variant="ink"
                    size="sm"
                    shape="app"
                    disabled={busy}
                    onClick={() => void handleConfirmReceived(activeReturn)}
                  >
                    {busy ? "Đang xử lý..." : "Xác nhận đã nhận đúng hàng"}
                  </Button>
                </div>
              </div>
            )}

            {/* Timeline */}
            {Array.isArray(activeReturn.events) && activeReturn.events.length > 0 && (
              <div className={styles.well}>
                <span className={styles.wellHeading}>Lịch sử xử lý</span>
                <div className={styles.timelineList}>
                  {activeReturn.events.map((ev) => {
                    const toInfo = getReturnStatusInfo(ev.to_status);
                    const localized = localizeTimelineNote(ev.note);
                    return (
                      <div key={ev.id} className={styles.timelineItem}>
                        <div className={styles.timelineHead}>
                          <span className={styles.timelineTitle}>{toInfo.label}</span>
                          <span className={styles.timelineTime}>
                            {ev.created_at ? formatDateTimeVi(ev.created_at) : EMPTY_DASH}
                          </span>
                        </div>
                        {localized && <span className={styles.timelineNote}>{localized}</span>}
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        )}
      </SellerDrawer>
    </div>
  );
}
