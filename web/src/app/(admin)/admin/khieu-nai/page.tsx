"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import type {
  GatewayReturnRequest,
  PaginatedResponse,
  ReturnAdminDecision,
  ReturnFeeBearer,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import {
  buildShippingLabelHref,
  getReturnFeeBearerLabel,
  localizeTimelineNote,
  normalizeEnumSuffix,
  parseSafeEvidenceUrl,
} from "@/features/account/orderMeta";
import { getAdminReturnStatusMeta } from "@/features/admin/types";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { useAdminCapability } from "@/features/admin/AdminUserContext";
import { StatusPill } from "@/features/admin/StatusPill";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ReturnFilterTab =
  | "all"
  | "ESCALATED"
  | "EVIDENCE_REQUESTED"
  | "PENDING"
  | "AWAITING_SHIPMENT"
  | "RECEIVED"
  | "REFUNDED"
  | "CLOSED";

const PAGE_SIZE = 20;

const TABS: Array<{ id: ReturnFilterTab; label: string }> = [
  { id: "all", label: "Tất cả" },
  { id: "ESCALATED", label: "Cần phân xử" },
  { id: "EVIDENCE_REQUESTED", label: "Chờ bằng chứng" },
  { id: "PENDING", label: "Chờ gian hàng" },
  { id: "AWAITING_SHIPMENT", label: "Chờ gửi trả" },
  { id: "RECEIVED", label: "Shop đã nhận" },
  { id: "REFUNDED", label: "Đã hoàn tiền" },
  { id: "CLOSED", label: "Đã đóng" },
];

function normalizeReturnStatus(raw?: string): string {
  return normalizeEnumSuffix(raw, "RETURN_STATUS_");
}

function AdminReturnsContent({
  initialQuery,
  initialSelected,
}: {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const { showToast } = useMockStore();
  const canResolveReturn = useAdminCapability("returns.resolve");

  const [activeTab, setActiveTab] = useState<ReturnFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [page, setPage] = useState(1);

  const [items, setItems] = useState<GatewayReturnRequest[]>([]);
  const [total, setTotal] = useState(0);
  const [escalatedCount, setEscalatedCount] = useState(0);
  const [evidenceCount, setEvidenceCount] = useState(0);
  const [pendingCount, setPendingCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [mobileOpenId, setMobileOpenId] = useState<string | null>(initialSelected);
  const [detail, setDetail] = useState<GatewayReturnRequest | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const [decision, setDecision] = useState<ReturnAdminDecision>("REFUND");
  const [feeBearer, setFeeBearer] = useState<ReturnFeeBearer>("SELLER");
  const [resolveNote, setResolveNote] = useState("");
  const [resolving, setResolving] = useState(false);
  const [resolveError, setResolveError] = useState<string | null>(null);

  const loadList = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    try {
      const qs = new URLSearchParams();
      qs.set("page", String(page));
      qs.set("page_size", String(PAGE_SIZE));
      if (activeTab !== "all") {
        qs.set("status", activeTab);
      }

      const [listRes, escRes, evRes, pendRes] = await Promise.all([
        api<PaginatedResponse<GatewayReturnRequest>>(`admin/returns?${qs.toString()}`),
        api<PaginatedResponse<GatewayReturnRequest>>(
          "admin/returns?status=ESCALATED&page=1&page_size=1"
        ).catch(() => null),
        api<PaginatedResponse<GatewayReturnRequest>>(
          "admin/returns?status=EVIDENCE_REQUESTED&page=1&page_size=1"
        ).catch(() => null),
        api<PaginatedResponse<GatewayReturnRequest>>(
          "admin/returns?status=PENDING&page=1&page_size=1"
        ).catch(() => null),
      ]);

      const fetched = listRes.items ?? [];
      setItems(fetched);
      setTotal(listRes.total ?? fetched.length);
      if (escRes) setEscalatedCount(escRes.total ?? escRes.items.length);
      if (evRes) setEvidenceCount(evRes.total ?? evRes.items.length);
      if (pendRes) setPendingCount(pendRes.total ?? pendRes.items.length);

      setSelectedId((prev) => {
        if (prev && fetched.some((r) => r.id === prev)) return prev;
        return fetched[0]?.id ?? null;
      });
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách khiếu nại trả hàng lúc này."));
    } finally {
      setLoading(false);
    }
  }, [activeTab, page]);

  useEffect(() => {
    void loadList();
  }, [loadList]);

  const loadDetail = useCallback(async (id: string) => {
    setDetailLoading(true);
    setResolveError(null);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `admin/returns/${encodeURIComponent(id)}`
      );
      if (res?.return_request) {
        setDetail(res.return_request);
        const bearer = (res.return_request.return_fee_bearer || "").toUpperCase();
        if (bearer === "SELLER" || bearer === "BUYER" || bearer === "PLATFORM") {
          setFeeBearer(bearer);
        } else {
          setFeeBearer("SELLER");
        }
        setDecision((prev) =>
          res.return_request.goods_received && prev === "RETURN_AND_REFUND" ? "REFUND" : prev
        );
      }
    } catch {
      // Keep list summary fallback if detail fetch fails
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (item) =>
        item.id.toLowerCase().includes(q) ||
        item.order_code.toLowerCase().includes(q) ||
        item.order_id.toLowerCase().includes(q) ||
        item.reason.toLowerCase().includes(q) ||
        (item.return_tracking_code ?? "").toLowerCase().includes(q)
    );
  }, [items, query]);

  const activeDesktopItem = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const found = filtered.find((r) => r.id === selectedId);
      if (found) return found;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  const activeInspectId = activeDesktopItem?.id ?? mobileOpenId;

  useEffect(() => {
    if (!activeInspectId) {
      setDetail(null);
      return;
    }
    void loadDetail(activeInspectId);
  }, [activeInspectId, loadDetail]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const handleSelectDesktop = (item: GatewayReturnRequest) => {
    setSelectedId(item.id);
    setResolveError(null);
    setResolveNote("");
  };

  const handleToggleMobile = (item: GatewayReturnRequest) => {
    setMobileOpenId((prev) => {
      const next = prev === item.id ? null : item.id;
      if (next) {
        setSelectedId(next);
        setResolveError(null);
        setResolveNote("");
      }
      return next;
    });
  };

  const handleResolveSubmit = async (e: React.FormEvent, target: GatewayReturnRequest) => {
    e.preventDefault();
    if (resolving) return;
    const cleanNote = resolveNote.trim();
    if ((decision === "REJECT" || decision === "REQUEST_EVIDENCE") && cleanNote.length < 4) {
      setResolveError("Vui lòng nhập ghi chú phân xử tối thiểu 4 ký tự cho quyết định này.");
      return;
    }
    if (decision === "RETURN_AND_REFUND" && target.goods_received) {
      setResolveError(
        "Gian hàng đã nhận hàng hoàn về nên không thể chọn yêu cầu gửi trả hàng mới."
      );
      return;
    }

    setResolveError(null);
    setResolving(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `admin/returns/${encodeURIComponent(target.id)}/resolve`,
        {
          method: "POST",
          json: {
            decision,
            note: cleanNote,
            return_fee_bearer: feeBearer,
          },
        }
      );
      if (res?.return_request) {
        setDetail(res.return_request);
        setItems((prev) =>
          prev.map((row) => (row.id === res.return_request.id ? res.return_request : row))
        );
      }
      setResolveNote("");
      showToast(`Đã lưu quyết định phân xử cho đơn ${target.order_code}.`);
      await loadList();
    } catch (err) {
      setResolveError(getErrorMessage(err, "Chưa thể gửi quyết định phân xử lúc này."));
    } finally {
      setResolving(false);
    }
  };

  const renderDetailPanel = (summaryItem: GatewayReturnRequest, showHeading = false) => {
    const record = detail && detail.id === summaryItem.id ? detail : summaryItem;
    const statusKey = normalizeReturnStatus(record.status);
    const meta = getAdminReturnStatusMeta(statusKey);
    const canResolve = statusKey === "ESCALATED" && canResolveReturn;
    const buyerUrls = (record.evidence_urls ?? [])
      .map((u) => parseSafeEvidenceUrl(u))
      .filter((x): x is NonNullable<typeof x> => x !== null);
    const sellerUrls = (record.seller_evidence_urls ?? [])
      .map((u) => parseSafeEvidenceUrl(u))
      .filter((x): x is NonNullable<typeof x> => x !== null);
    const returnLabelHref = buildShippingLabelHref(record.return_tracking_code);
    const sortedEvents = [...(record.events ?? [])].sort((a, b) => {
      const ta = a.created_at ? Date.parse(a.created_at) : 0;
      const tb = b.created_at ? Date.parse(b.created_at) : 0;
      return tb - ta;
    });

    return (
      <div className={shared.detailWell}>
        {showHeading && (
          <div className={shared.detailHeader}>
            <h2 className={shared.detailHeading}>
              Hồ sơ trả hàng / khiếu nại: Đơn {record.order_code}
            </h2>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
            </div>
          </div>
        )}

        {detailLoading && (!detail || detail.id !== summaryItem.id) && (
          <p className={styles.loadingNote}>Đang tải chi tiết bằng chứng và lịch sử hồ sơ...</p>
        )}

        <div className={shared.detailGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã hồ sơ / Mã đơn</span>
            <span className={`${shared.detailVal} num`}>
              {record.order_code} · {record.id.slice(0, 8)}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Số tiền dự kiến hoàn</span>
            <span className={`${shared.detailVal} num`}>{formatVnd(record.refund_amount_vnd)}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Trạng thái hàng hoàn</span>
            <span className={shared.detailVal}>
              {record.goods_received ? "Gian hàng đã nhận hàng trả" : "Chưa nhận hàng hoàn về"}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Vận đơn chiều trả hàng</span>
            <span className={`${shared.detailValPlain} num`}>
              {record.return_tracking_code ? (
                returnLabelHref ? (
                  <>
                    <a
                      href={returnLabelHref}
                      target="_blank"
                      rel="noopener noreferrer"
                      className={styles.evidenceLink}
                      title="Mở nhãn gửi trả ở tab mới — nhấn Ctrl+P để in"
                    >
                      {record.return_tracking_code} (In nhãn gửi trả)
                    </a>{" "}
                    <span className={styles.mutedLine}>(Ctrl+P để in)</span>
                  </>
                ) : (
                  record.return_tracking_code
                )
              ) : (
                "Chưa tạo vận đơn chiều trả"
              )}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Phí trả hàng & bên chịu phí</span>
            <span className={`${shared.detailValPlain} num`}>
              {record.return_fee_vnd > 0 ? formatVnd(record.return_fee_vnd) : "0₫"} ·{" "}
              {record.return_fee_bearer
                ? getReturnFeeBearerLabel(record.return_fee_bearer)
                : "Chưa ấn định"}
            </span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Hạn xử lý bước hiện tại</span>
            <span className={`${shared.detailValPlain} num`}>
              {record.deadline_at
                ? formatDateTimeVi(record.deadline_at)
                : "Không giới hạn hạn chót"}
            </span>
          </div>
        </div>

        {/* Two-sided evidence presentation */}
        <div className={styles.sidesGrid}>
          <div className={styles.sideBox}>
            <h3 className={styles.sideHeading}>Phía người mua</h3>
            <p className={styles.sideText}>{record.reason || "Chưa có mô tả lý do."}</p>
            {buyerUrls.length > 0 ? (
              <ul className={styles.evidenceList}>
                {buyerUrls.map((ev, idx) => (
                  <li key={`${ev.href}-${idx}`}>
                    <a
                      href={ev.href}
                      target="_blank"
                      rel="noopener noreferrer nofollow"
                      className={styles.evidenceLink}
                    >
                      Bằng chứng người mua #{idx + 1} ({ev.hostname}) — trang ngoài
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className={styles.mutedLine}>Không đính kèm liên kết bằng chứng.</p>
            )}
          </div>

          <div className={styles.sideBox}>
            <h3 className={styles.sideHeading}>Phía gian hàng</h3>
            <p className={styles.sideText}>
              {record.seller_response || "Gian hàng chưa gửi phản hồi chi tiết."}
            </p>
            {record.dispute_reason && (
              <p className={styles.disputeCallout}>
                <strong>Nội dung khiếu nại / báo sai hàng:</strong> {record.dispute_reason}
              </p>
            )}
            {sellerUrls.length > 0 ? (
              <ul className={styles.evidenceList}>
                {sellerUrls.map((ev, idx) => (
                  <li key={`${ev.href}-${idx}`}>
                    <a
                      href={ev.href}
                      target="_blank"
                      rel="noopener noreferrer nofollow"
                      className={styles.evidenceLink}
                    >
                      Chứng từ gian hàng #{idx + 1} ({ev.hostname}) — trang ngoài
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className={styles.mutedLine}>Gian hàng chưa đính kèm chứng từ.</p>
            )}
          </div>
        </div>

        {record.admin_note && (
          <div className={styles.adminNoteBox}>
            <span className={shared.detailKey}>Ghi chú phân xử gần nhất của quản trị viên</span>
            <p className={shared.detailValPlain}>{record.admin_note}</p>
          </div>
        )}

        {/* Resolution form for ESCALATED returns */}
        {canResolve ? (
          <form
            className={styles.resolveForm}
            onSubmit={(e) => void handleResolveSubmit(e, record)}
          >
            <div className={styles.resolveGrid}>
              <div className={shared.detailField}>
                <label htmlFor={`ret-decision-${record.id}`} className={shared.fieldLabel}>
                  Quyết định phân xử
                </label>
                <select
                  id={`ret-decision-${record.id}`}
                  className={styles.fullSelect}
                  value={decision}
                  onChange={(e) => setDecision(e.target.value as ReturnAdminDecision)}
                  disabled={resolving}
                >
                  <option value="REFUND">Hoàn tiền người mua (không yêu cầu gửi thêm hàng)</option>
                  <option value="RETURN_AND_REFUND" disabled={record.goods_received}>
                    Yêu cầu trả hàng rồi hoàn tiền
                    {record.goods_received ? " (không áp dụng vì shop đã nhận hàng)" : ""}
                  </option>
                  <option value="REQUEST_EVIDENCE">
                    Yêu cầu người mua bổ sung bằng chứng (hạn 3 ngày)
                  </option>
                  <option value="REJECT">Từ chối khiếu nại & đóng hồ sơ</option>
                </select>
              </div>

              <div className={shared.detailField}>
                <label htmlFor={`ret-bearer-${record.id}`} className={shared.fieldLabel}>
                  Bên chịu phí vận chuyển trả hàng
                </label>
                <select
                  id={`ret-bearer-${record.id}`}
                  className={styles.fullSelect}
                  value={feeBearer}
                  onChange={(e) => setFeeBearer(e.target.value as ReturnFeeBearer)}
                  disabled={resolving}
                >
                  <option value="SELLER">Gian hàng chịu phí</option>
                  <option value="BUYER">Người mua chịu phí</option>
                  <option value="PLATFORM">Sàn NexusMart chịu phí</option>
                </select>
              </div>

              <div className={`${shared.detailField} ${styles.resolveWide}`}>
                <label htmlFor={`ret-note-${record.id}`} className={shared.fieldLabel}>
                  Kết luận & căn cứ phân xử
                  {decision === "REJECT" || decision === "REQUEST_EVIDENCE"
                    ? " (bắt buộc)"
                    : " (khuyến nghị)"}
                </label>
                <textarea
                  id={`ret-note-${record.id}`}
                  className={shared.textareaInput}
                  value={resolveNote}
                  onChange={(e) => setResolveNote(e.target.value)}
                  placeholder="Ghi rõ căn cứ kiểm tra bằng chứng hai bên, lý do phân bổ phí trả hàng hoặc nội dung cần bổ sung..."
                  disabled={resolving}
                />
              </div>
            </div>

            {resolveError && (
              <p className={styles.validationError} role="alert">
                {resolveError}
              </p>
            )}

            <div className={shared.detailActionBar}>
              <span className={shared.savedStamp}>
                Quyết định sẽ cập nhật ngay trạng thái đơn hàng, kích hoạt hoàn tiền hoặc tạo vận
                đơn chiều trả tương ứng.
              </span>
              <div className={shared.detailButtons}>
                <Button type="submit" variant="primary" size="sm" shape="app" disabled={resolving}>
                  {resolving ? "Đang lưu quyết định..." : "Xác nhận quyết định phân xử"}
                </Button>
              </div>
            </div>
          </form>
        ) : (
          <div className={shared.detailActionBar}>
            <span className={statusKey === "REFUNDED" ? styles.resolvedNote : shared.savedStamp}>
              {statusKey === "EVIDENCE_REQUESTED"
                ? "Đang chờ người mua gửi bổ sung bằng chứng trước hạn chót; khi nhận đủ, hồ sơ sẽ quay lại trạng thái Cần phân xử."
                : statusKey === "REFUNDED" || statusKey === "REFUNDING"
                  ? "Hồ sơ đã chuyển sang bước hoàn tiền cho người mua."
                  : statusKey === "CLOSED"
                    ? "Hồ sơ đã đóng và hoàn tất quyết toán đơn hàng."
                    : "Hồ sơ hiện đang ở bước xử lý giữa người mua và gian hàng, chưa cần quản trị viên can thiệp."}
            </span>
          </div>
        )}

        {/* Timeline */}
        {sortedEvents.length > 0 && (
          <div className={styles.timelineBlock}>
            <h3 className={styles.sideHeading}>Lịch sử chuyển trạng thái hồ sơ</h3>
            <ol className={styles.timelineList}>
              {sortedEvents.map((ev, idx) => {
                const evMeta = getAdminReturnStatusMeta(ev.to_status);
                return (
                  <li
                    key={ev.id || `${ev.created_at ?? idx}-${idx}`}
                    className={styles.timelineItem}
                  >
                    <span className={`${styles.timelineTime} num`}>
                      {ev.created_at ? formatDateTimeVi(ev.created_at) : "—"}
                    </span>
                    <div className={styles.timelineBody}>
                      <span className={styles.timelineTitle}>{evMeta.label}</span>
                      {ev.note && (
                        <span className={styles.timelineNote}>{localizeTimelineNote(ev.note)}</span>
                      )}
                    </div>
                  </li>
                );
              })}
            </ol>
          </div>
        )}
      </div>
    );
  };

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Khiếu nại & Yêu cầu trả hàng</h1>
          <p className={shared.pageSubtitle}>
            Theo dõi toàn bộ hồ sơ trả hàng trên sàn và phân xử các vụ khiếu nại hai bên trực tiếp
            từ dịch vụ đơn hàng.
          </p>
        </div>
        <div className={shared.titleActions}>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadList()}>
            Làm mới
          </Button>
        </div>
      </header>

      <section
        className={`${shared.kpiStrip} ${styles.kpiSpacer}`}
        aria-label="Tổng hợp hồ sơ trả hàng và khiếu nại"
      >
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Cần quản trị phân xử</span>
          <span
            className={`${shared.kpiValue} ${escalatedCount > 0 ? shared.kpiValueNegative : ""}`}
          >
            {formatInt(escalatedCount)}
          </span>
          <span className={shared.kpiCaption}>Hồ sơ khiếu nại đang mở</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chờ bổ sung bằng chứng</span>
          <span className={shared.kpiValue}>{formatInt(evidenceCount)}</span>
          <span className={shared.kpiCaption}>Đang chờ người mua phản hồi trong 3 ngày</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Chờ gian hàng phản hồi</span>
          <span className={shared.kpiValue}>{formatInt(pendingCount)}</span>
          <span className={shared.kpiCaption}>Hạn 3 ngày trước khi tự động đồng ý trả hàng</span>
        </div>
        <div className={shared.kpiCell}>
          <span className={shared.kpiLabel}>Kết quả theo bộ lọc</span>
          <span className={shared.kpiValue}>{formatInt(total)}</span>
          <span className={shared.kpiCaption}>
            Trang {formatInt(page)} / {formatInt(totalPages)}
          </span>
        </div>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái hồ sơ trả hàng">
          {TABS.map((tab) => {
            const active = activeTab === tab.id;
            const badge =
              tab.id === "ESCALATED"
                ? escalatedCount
                : tab.id === "EVIDENCE_REQUESTED"
                  ? evidenceCount
                  : tab.id === "PENDING"
                    ? pendingCount
                    : null;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => {
                  setActiveTab(tab.id);
                  setPage(1);
                }}
              >
                <span>{tab.label}</span>
                {badge != null && badge > 0 && <span className={shared.tabCount}>{badge}</span>}
              </button>
            );
          })}
        </div>
      </div>

      <div className={shared.toolbar}>
        <div className={shared.toolbarLeft}>
          <div className={shared.searchWrap}>
            <span className={shared.searchIcon}>
              <IconSearch size={14} />
            </span>
            <input
              type="search"
              className={shared.toolbarSearch}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Tìm mã đơn NM-, mã vận đơn trả, lý do..."
              aria-label="Tìm kiếm hồ sơ trả hàng và khiếu nại"
            />
          </div>
        </div>
        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>
            {formatInt(filtered.length)} / {formatInt(total)} hồ sơ
          </span>
        </div>
      </div>

      {loading ? (
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tải danh sách khiếu nại trả hàng..."
            rows={5}
          />
        </div>
      ) : errorMsg ? (
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải danh sách khiếu nại"
            message={errorMsg}
            actionLabel="Thử lại"
            onAction={() => void loadList()}
          />
        </div>
      ) : filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không có hồ sơ trả hàng hoặc khiếu nại nào khớp bộ lọc.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Đơn hàng & lý do yêu cầu</th>
                  <th className={shared.colNum}>Tiền hoàn dự kiến</th>
                  <th className={styles.tabletHideCol}>Hạn xử lý / Vận đơn trả</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((item) => {
                  const statusKey = normalizeReturnStatus(item.status);
                  const meta = getAdminReturnStatusMeta(statusKey);
                  const isSelected = activeDesktopItem?.id === item.id;
                  return (
                    <tr
                      key={item.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => handleSelectDesktop(item)}
                    >
                      <td>
                        <div className={shared.cellPrimary}>
                          {item.order_code} · #{item.id.slice(0, 8)}
                        </div>
                        <div className={`${shared.cellSub} ${styles.reasonCell}`}>
                          {item.dispute_reason || item.reason}
                        </div>
                      </td>
                      <td className={shared.colNum}>{formatVnd(item.refund_amount_vnd)}</td>
                      <td className={styles.tabletHideCol}>
                        <div className="num">
                          {item.deadline_at ? formatDateTimeVi(item.deadline_at) : "—"}
                        </div>
                        <div className={shared.cellSub}>
                          {item.return_tracking_code
                            ? `Vận đơn trả: ${item.return_tracking_code}`
                            : item.goods_received
                              ? "Shop đã nhận hàng"
                              : "Chưa tạo vận đơn trả"}
                        </div>
                      </td>
                      <td>
                        <StatusPill label={meta.label} tone={meta.tone} />
                      </td>
                      <td className={shared.colAction}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={(e) => {
                            e.stopPropagation();
                            handleSelectDesktop(item);
                          }}
                        >
                          {isSelected
                            ? "Đang xem"
                            : statusKey === "ESCALATED"
                              ? "Phân xử"
                              : "Chi tiết"}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>

          {activeDesktopItem && (
            <section
              className={shared.desktopDetailSection}
              aria-label="Bảng chi tiết và phân xử khiếu nại trả hàng"
            >
              {renderDetailPanel(activeDesktopItem, true)}
            </section>
          )}

          <div className={shared.mobileList}>
            {filtered.map((item) => {
              const statusKey = normalizeReturnStatus(item.status);
              const meta = getAdminReturnStatusMeta(statusKey);
              const isOpen = mobileOpenId === item.id;
              return (
                <div key={item.id} className={shared.mobileItem}>
                  <button
                    type="button"
                    className={shared.mobileItemBtn}
                    onClick={() => handleToggleMobile(item)}
                    aria-expanded={isOpen}
                  >
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{item.order_code}</span>
                      <span className={shared.mobileMetric}>
                        {formatVnd(item.refund_amount_vnd)}
                      </span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {item.dispute_reason || item.reason}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                  </button>
                  {isOpen && (
                    <div className={shared.mobileDetailWrap}>{renderDetailPanel(item)}</div>
                  )}
                </div>
              );
            })}
          </div>

          {totalPages > 1 && (
            <div className={styles.paginationRow}>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                Trang trước
              </Button>
              <span className={shared.resultCount}>
                Trang {formatInt(page)} / {formatInt(totalPages)}
              </span>
              <Button
                variant="outline"
                size="sm"
                shape="app"
                disabled={page >= totalPages}
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              >
                Trang sau
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function AdminReturnsRoute() {
  return (
    <AdminStateGate
      title="Khiếu nại & Yêu cầu trả hàng"
      subtitle="Phân xử tranh chấp trả hàng giữa người mua và gian hàng dựa trên bằng chứng hai bên."
      emptyTitle="Chưa có hồ sơ khiếu nại nào"
      emptyMessage="Tất cả yêu cầu trả hàng đã được xử lý hoặc chưa có khiếu nại mới phát sinh."
      errorTitle="Không thể tải danh sách khiếu nại"
      errorMessage="Dịch vụ đơn hàng tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery, initialSelected }) => (
        <AdminReturnsContent initialQuery={initialQuery} initialSelected={initialSelected} />
      )}
    </AdminStateGate>
  );
}

export default function AdminDisputesPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Khiếu nại & Yêu cầu trả hàng" />}>
      <AdminReturnsRoute />
    </Suspense>
  );
}
