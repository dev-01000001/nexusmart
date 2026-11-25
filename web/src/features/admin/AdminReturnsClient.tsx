"use client";

import React, { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Button, IconChevronLeft, IconChevronRight, IconSearch, StateView } from "@/components";
import { hasOpenModal } from "@/components/modalFocus";
import { api, getErrorMessage } from "@/lib/api";
import { roleHasCapability } from "@/lib/admin-rbac";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import type {
  GatewayReturnRequest,
  PaginatedResponse,
  ReturnAdminDecision,
  ReturnFeeBearer,
} from "@/lib/gateway-types";
import {
  buildShippingLabelHref,
  getTimelineEventTitle,
  isAddressUpdatedTimelineNote,
  localizeTimelineNote,
  parseSafeEvidenceUrl,
} from "@/features/account/orderMeta";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { AdminDrawer, SlidingTabBar, isEditableElement } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import { getAdminReturnStatusMeta } from "./types";
import {
  canAdminResolveReturn,
  getReturnDecisionLabelVi,
  getReturnFeeBearerLabelVi,
  validateReturnResolveInput,
} from "./AdminReturnsHelpers";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/khieu-nai/page.module.css";

type ReturnFilterTab =
  | ""
  | "ESCALATED"
  | "EVIDENCE_REQUESTED"
  | "PENDING"
  | "AWAITING_SHIPMENT"
  | "RECEIVED"
  | "REFUNDING"
  | "REFUNDED"
  | "CLOSED";

const PAGE_SIZE = 20;

const DECISION_OPTIONS: Array<{
  value: ReturnAdminDecision;
  title: string;
  hint: string;
}> = [
  {
    value: "REFUND",
    title: "Hoàn tiền ngay",
    hint: "Tạo lệnh hoàn tiền trực tiếp, không yêu cầu gửi trả hàng.",
  },
  {
    value: "RETURN_AND_REFUND",
    title: "Trả hàng và hoàn tiền",
    hint: "Tạo vận đơn hoàn về gian hàng, hoàn tiền sau khi nhận hàng.",
  },
  {
    value: "REQUEST_EVIDENCE",
    title: "Yêu cầu bổ sung bằng chứng",
    hint: "Gia hạn 3 ngày để người mua gửi thêm chứng từ xác minh.",
  },
  {
    value: "REJECT",
    title: "Từ chối khiếu nại",
    hint: "Đóng khiếu nại và chuyển đơn về trạng thái Hoàn tất.",
  },
];

const FEE_BEARER_OPTIONS: Array<{ value: ReturnFeeBearer; label: string }> = [
  { value: "SELLER", label: "Gian hàng chịu phí hoàn hàng" },
  { value: "BUYER", label: "Người mua chịu phí hoàn hàng" },
  { value: "PLATFORM", label: "Sàn hỗ trợ phí hoàn hàng" },
];

export interface AdminReturnsClientProps {
  initialItems: GatewayReturnRequest[];
  initialTotal: number;
  initialEscalatedCount: number;
  initialEvidenceCount: number;
  loadError: string | null;
}

function ReturnsWorkspace({
  initialItems,
  initialTotal,
  initialEscalatedCount,
  initialEvidenceCount,
  loadError: initialLoadError,
  initialQuery,
  initialSelected,
}: AdminReturnsClientProps & {
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { user, showToast } = useAdminContext();

  const canResolve = roleHasCapability(user.role, "returns.resolve");
  const canOpenRefunds = roleHasCapability(user.role, "refunds.read");

  const [items, setItems] = useState<GatewayReturnRequest[]>(initialItems);
  const [total, setTotal] = useState(initialTotal);
  const [escalatedCount, setEscalatedCount] = useState(initialEscalatedCount);
  const [evidenceCount, setEvidenceCount] = useState(initialEvidenceCount);
  const [statusTab, setStatusTab] = useState<ReturnFilterTab>("");
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(initialLoadError);

  const [query, setQuery] = useState(initialQuery);
  const [selectedId, setSelectedId] = useState<string | null>(
    initialSelected ?? initialItems[0]?.id ?? null
  );
  const [detailById, setDetailById] = useState<Record<string, GatewayReturnRequest>>({});
  const [loadingDetailId, setLoadingDetailId] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const [decision, setDecision] = useState<ReturnAdminDecision>("REFUND");
  const [feeBearer, setFeeBearer] = useState<ReturnFeeBearer>("SELLER");
  const [note, setNote] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const rowRefs = useRef<Map<string, HTMLButtonElement>>(new Map());
  const listAbortRef = useRef<AbortController | null>(null);
  const detailAbortRef = useRef<AbortController | null>(null);
  const deepLinkHandledRef = useRef(false);

  useEffect(() => {
    return () => {
      listAbortRef.current?.abort();
      detailAbortRef.current?.abort();
    };
  }, []);

  const loadList = useCallback(async (nextStatus: ReturnFilterTab, nextPage: number) => {
    listAbortRef.current?.abort();
    const controller = new AbortController();
    listAbortRef.current = controller;

    setLoading(true);
    setLoadError(null);
    try {
      const qs = new URLSearchParams({
        page: String(nextPage),
        page_size: String(PAGE_SIZE),
      });
      if (nextStatus) qs.set("status", nextStatus);
      const res = await api<PaginatedResponse<GatewayReturnRequest>>(
        `/api/admin/returns?${qs.toString()}`,
        { signal: controller.signal }
      );
      if (controller.signal.aborted) return;
      const list = Array.isArray(res.items) ? res.items : [];
      setItems(list);
      setTotal(res.total ?? list.length);
      if (nextStatus === "ESCALATED") setEscalatedCount(res.total ?? list.length);
      if (nextStatus === "EVIDENCE_REQUESTED") setEvidenceCount(res.total ?? list.length);
      setSelectedId((prev) =>
        prev && list.some((r) => r.id === prev) ? prev : (list[0]?.id ?? null)
      );
    } catch (err) {
      if (controller.signal.aborted) return;
      setLoadError(getErrorMessage(err, "Không thể tải danh sách yêu cầu trả hàng lúc này."));
    } finally {
      if (!controller.signal.aborted) {
        setLoading(false);
      }
    }
  }, []);

  const fetchDetail = useCallback(async (returnId: string) => {
    detailAbortRef.current?.abort();
    const controller = new AbortController();
    detailAbortRef.current = controller;

    setLoadingDetailId(returnId);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `/api/admin/returns/${encodeURIComponent(returnId)}`,
        { signal: controller.signal }
      );
      if (controller.signal.aborted || !res?.return_request) return;
      setDetailById((prev) => ({ ...prev, [returnId]: res.return_request }));
      setItems((prev) =>
        prev.map((it) => (it.id === returnId ? { ...it, ...res.return_request } : it))
      );
    } catch {
      // Keep list-level snapshot if detail refresh fails.
    } finally {
      if (!controller.signal.aborted) {
        setLoadingDetailId((cur) => (cur === returnId ? null : cur));
      }
    }
  }, []);

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (r) =>
        r.id.toLowerCase().includes(q) ||
        (r.order_code ?? "").toLowerCase().includes(q) ||
        r.order_id.toLowerCase().includes(q) ||
        r.buyer_id.toLowerCase().includes(q) ||
        r.shop_id.toLowerCase().includes(q) ||
        (r.reason ?? "").toLowerCase().includes(q) ||
        (r.dispute_reason ?? "").toLowerCase().includes(q) ||
        (r.return_tracking_code ?? "").toLowerCase().includes(q)
    );
  }, [items, query]);

  const activeIndex = useMemo(() => {
    if (filteredItems.length === 0) return -1;
    if (selectedId) {
      const idx = filteredItems.findIndex((r) => r.id === selectedId);
      if (idx >= 0) return idx;
    }
    return 0;
  }, [filteredItems, selectedId]);

  const activeListItem = activeIndex >= 0 ? filteredItems[activeIndex] : null;
  const activeReturn = activeListItem ? (detailById[activeListItem.id] ?? activeListItem) : null;

  useEffect(() => {
    if (deepLinkHandledRef.current) return;
    deepLinkHandledRef.current = true;
    if (
      initialSelected &&
      typeof window !== "undefined" &&
      window.innerWidth <= 960 &&
      initialItems.some((r) => r.id === initialSelected)
    ) {
      setDrawerOpen(true);
    }
  }, [initialSelected, initialItems]);

  useEffect(() => {
    if (!activeListItem) {
      if (drawerOpen) setDrawerOpen(false);
      return;
    }
    if (!detailById[activeListItem.id]) {
      void fetchDetail(activeListItem.id);
    }
  }, [activeListItem, detailById, drawerOpen, fetchDetail]);

  useEffect(() => {
    setFormError(null);
    setNote("");
    setDecision("REFUND");
    setFeeBearer("SELLER");
  }, [activeReturn?.id]);

  const stepSelection = useCallback(
    (delta: 1 | -1) => {
      if (filteredItems.length === 0) return;
      const cur = activeIndex >= 0 ? activeIndex : 0;
      const nextIdx = Math.max(0, Math.min(filteredItems.length - 1, cur + delta));
      const nextItem = filteredItems[nextIdx];
      if (!nextItem) return;
      setSelectedId(nextItem.id);
      rowRefs.current.get(nextItem.id)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
    },
    [filteredItems, activeIndex]
  );

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || isEditableElement(e.target) || hasOpenModal()) {
        return;
      }
      const key = e.key.toLowerCase();
      if (key === "j") {
        e.preventDefault();
        stepSelection(1);
      } else if (key === "k") {
        e.preventDefault();
        stepSelection(-1);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [stepSelection]);

  const handleTabChange = (nextTab: ReturnFilterTab) => {
    setStatusTab(nextTab);
    setPage(1);
    void loadList(nextTab, 1);
  };

  const handlePageChange = (nextPage: number) => {
    setPage(nextPage);
    void loadList(statusTab, nextPage);
  };

  const handleResolveSubmit = async (e: React.FormEvent, target: GatewayReturnRequest) => {
    e.preventDefault();
    if (submitting || !canResolve) return;

    const validated = validateReturnResolveInput(
      {
        decision,
        note,
        return_fee_bearer:
          decision === "REFUND" || decision === "RETURN_AND_REFUND" ? feeBearer : "",
      },
      { goodsReceived: Boolean(target.goods_received) }
    );
    if (!validated.ok) {
      setFormError(validated.message);
      return;
    }

    setFormError(null);
    setSubmitting(true);
    try {
      const res = await api<{ return_request: GatewayReturnRequest }>(
        `/api/admin/returns/${encodeURIComponent(target.id)}/resolve`,
        {
          method: "POST",
          body: validated.value,
        }
      );
      const updated = res.return_request;
      setDetailById((prev) => ({ ...prev, [target.id]: updated }));
      setItems((prev) => prev.map((it) => (it.id === target.id ? updated : it)));
      setNote("");
      showToast(
        `Đã phân xử đơn ${target.order_code || target.order_id.slice(0, 8)}: ${getReturnDecisionLabelVi(validated.value.decision)}.`
      );
      router.refresh();
    } catch (err) {
      const msg = getErrorMessage(err, "Không thể gửi quyết định phân xử lúc này.");
      setFormError(msg);
      showToast(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const renderEvidenceLinks = (label: string, urls: string[] | undefined) => {
    const safeLinks = (urls ?? [])
      .map((raw) => parseSafeEvidenceUrl(raw))
      .filter((item): item is NonNullable<typeof item> => item !== null);
    return (
      <div className={styles.evidenceGroup}>
        <span className={shared.fieldLabel}>
          {label} ({formatInt(safeLinks.length)})
        </span>
        {safeLinks.length === 0 ? (
          <span className={styles.fieldHint}>Chưa đính kèm bằng chứng.</span>
        ) : (
          <ul className={styles.evidenceList}>
            {safeLinks.map((link, idx) => (
              <li key={`${link.href}-${idx}`}>
                <a
                  href={link.href}
                  target="_blank"
                  rel="noopener noreferrer nofollow"
                  className={styles.evidenceLink}
                >
                  <span>Bằng chứng #{idx + 1}</span>
                  <span className={styles.evidenceHost}>({link.hostname})</span>
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  };

  const renderInspector = (ret: GatewayReturnRequest, inDrawer = false) => {
    const meta = getAdminReturnStatusMeta(ret.status);
    const resolvable = canAdminResolveReturn(ret.status);
    const labelHref = buildShippingLabelHref(ret.return_tracking_code, ret.order_id);
    const isRefundingStage =
      ret.status.toUpperCase() === "REFUNDING" || ret.status.toUpperCase() === "REFUNDED";
    const noteId = `ret-note-${inDrawer ? "drawer" : "pane"}-${ret.id}`;
    const feeId = `ret-fee-${inDrawer ? "drawer" : "pane"}-${ret.id}`;

    return (
      <div
        key={ret.id}
        className={`${inDrawer ? styles.drawerContent : shared.detailWellCard} ${shared.inspectorSlide}`}
      >
        {!inDrawer && (
          <div className={shared.detailHeader}>
            <div>
              <h2 className={shared.detailHeading}>
                Đơn {ret.order_code || ret.order_id.slice(0, 8)}
              </h2>
              <span className={shared.cellSub}>
                Mã yêu cầu {ret.id.slice(0, 12)}
                {ret.created_at ? `, tạo lúc ${formatDateTimeVi(ret.created_at)}` : ""}
              </span>
            </div>
            <div className={shared.detailHeaderMeta}>
              <StatusPill label={meta.label} tone={meta.tone} />
              <div className={styles.stepperBtns}>
                <button
                  type="button"
                  className={styles.stepperBtn}
                  disabled={activeIndex <= 0}
                  onClick={() => stepSelection(-1)}
                  aria-label="Hồ sơ trước (K)"
                  title="Hồ sơ trước (K)"
                >
                  <IconChevronLeft size={14} />
                </button>
                <button
                  type="button"
                  className={styles.stepperBtn}
                  disabled={activeIndex >= filteredItems.length - 1}
                  onClick={() => stepSelection(1)}
                  aria-label="Hồ sơ tiếp theo (J)"
                  title="Hồ sơ tiếp theo (J)"
                >
                  <IconChevronRight size={14} />
                </button>
              </div>
            </div>
          </div>
        )}

        <div className={shared.detailWell}>
          <div className={shared.detailGrid}>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Số tiền hoàn dự kiến</span>
              <span className={`${shared.detailVal} num`}>
                {ret.refund_amount_vnd > 0 ? formatVnd(ret.refund_amount_vnd) : EMPTY_DASH}
              </span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Phí hoàn hàng</span>
              <span className={shared.detailValPlain}>
                {ret.return_fee_vnd > 0 ? `${formatVnd(ret.return_fee_vnd)} · ` : ""}
                {getReturnFeeBearerLabelVi(ret.return_fee_bearer)}
              </span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Người mua</span>
              <span className={`${shared.detailValPlain} num`}>{ret.buyer_id.slice(0, 12)}</span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Gian hàng</span>
              <span className={`${shared.detailValPlain} num`}>{ret.shop_id.slice(0, 12)}</span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Tình trạng hàng trả</span>
              <span className={shared.detailValPlain}>
                {ret.goods_received ? "Gian hàng đã nhận hàng trả" : "Chưa nhận hàng trả"}
              </span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Hạn xử lý</span>
              <span className={`${shared.detailValPlain} num`}>
                {ret.deadline_at ? formatDateTimeVi(ret.deadline_at) : EMPTY_DASH}
              </span>
            </div>
            {ret.return_tracking_code && (
              <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                <span className={shared.detailKey}>Vận đơn hoàn hàng</span>
                <span className={`${shared.detailValPlain} num`}>
                  {ret.return_tracking_code}
                  {labelHref && (
                    <>
                      {" · "}
                      <a
                        href={labelHref}
                        target="_blank"
                        rel="noopener noreferrer"
                        className={styles.noticeLink}
                        title="Mở nhãn vận đơn ở tab mới — nhấn Ctrl+P để in"
                      >
                        In nhãn vận đơn (Ctrl+P)
                      </a>
                    </>
                  )}
                </span>
              </div>
            )}
          </div>
        </div>

        <div className={styles.reasonBlock}>
          <div className={styles.statementBox}>
            <span className={styles.statementTitle}>Lý do người mua yêu cầu trả hàng</span>
            <p className={styles.statementBody}>{ret.reason || "Chưa có mô tả chi tiết."}</p>
          </div>
          {ret.seller_response && (
            <div className={styles.statementBox}>
              <span className={styles.statementTitle}>Phản hồi từ gian hàng</span>
              <p className={styles.statementBody}>{ret.seller_response}</p>
            </div>
          )}
          {ret.dispute_reason && (
            <div className={styles.statementBox}>
              <span className={styles.statementTitle}>Nội dung khiếu nại lên sàn</span>
              <p className={styles.statementBody}>{ret.dispute_reason}</p>
            </div>
          )}
          {ret.admin_note && (
            <div className={styles.statementBox}>
              <span className={styles.statementTitle}>Ghi chú quản trị viên</span>
              <p className={styles.statementBody}>{ret.admin_note}</p>
            </div>
          )}
        </div>

        {renderEvidenceLinks("Bằng chứng người mua", ret.evidence_urls)}
        {renderEvidenceLinks("Bằng chứng gian hàng", ret.seller_evidence_urls)}

        {isRefundingStage && (
          <div className={styles.noticeBanner} role="status">
            <span>
              Các lệnh hoàn tiền giá trị lớn tự động chuyển sang quy trình duyệt 2 người
              (maker–checker) trước khi tất toán.
            </span>
            {canOpenRefunds && (
              <Link href="/admin/doi-soat?tab=refunds" className={styles.noticeLink}>
                Theo dõi &amp; duyệt lệnh hoàn tiền tại Đối soát
              </Link>
            )}
          </div>
        )}

        {loadingDetailId === ret.id && (
          <span className={styles.fieldHint}>Đang tải chi tiết lịch sử xử lý...</span>
        )}

        {(ret.events?.length ?? 0) > 0 && (
          <div className={styles.evidenceGroup}>
            <span className={shared.fieldLabel}>Diễn biến xử lý</span>
            <ul className={styles.timelineList}>
              {ret.events.map((ev) => {
                const evMeta = getAdminReturnStatusMeta(ev.to_status);
                const title = isAddressUpdatedTimelineNote(ev.note)
                  ? getTimelineEventTitle({
                      from_status: ev.from_status,
                      to_status: ev.to_status,
                      note: ev.note,
                    })
                  : evMeta.label;
                return (
                  <li key={ev.id} className={styles.timelineItem}>
                    <div className={styles.timelineHead}>
                      <span className={styles.timelineActor}>{title}</span>
                      <span className={styles.timelineTime}>
                        {ev.created_at ? formatShortTimeDateVi(ev.created_at) : EMPTY_DASH}
                      </span>
                    </div>
                    {ev.note && (
                      <p className={styles.timelineNote}>{localizeTimelineNote(ev.note)}</p>
                    )}
                  </li>
                );
              })}
            </ul>
          </div>
        )}

        {resolvable && canResolve && (
          <form
            className={styles.decisionForm}
            onSubmit={(e) => void handleResolveSubmit(e, ret)}
            noValidate
          >
            <h3 className={styles.decisionHeading}>Quyết định phân xử</h3>

            <div
              className={styles.radioGroup}
              role="radiogroup"
              aria-label="Chọn quyết định phân xử trả hàng"
            >
              {DECISION_OPTIONS.map((opt) => {
                const disabled = opt.value === "RETURN_AND_REFUND" && Boolean(ret.goods_received);
                const checked = decision === opt.value;
                return (
                  <label
                    key={opt.value}
                    className={`${styles.radioOption} ${checked ? styles.radioOptionSelected : ""} ${disabled ? styles.radioOptionDisabled : ""}`}
                  >
                    <input
                      type="radio"
                      name={`return-decision-${inDrawer ? "drawer" : "pane"}-${ret.id}`}
                      value={opt.value}
                      checked={checked}
                      disabled={disabled || submitting}
                      onChange={() => {
                        setDecision(opt.value);
                        setFormError(null);
                      }}
                      className={styles.radioInput}
                    />
                    <span className={styles.radioText}>
                      <span className={styles.radioTitle}>{opt.title}</span>
                      <span className={styles.radioHint}>
                        {disabled
                          ? "Gian hàng đã nhận hàng trả nên không tạo lại vận đơn."
                          : opt.hint}
                      </span>
                    </span>
                  </label>
                );
              })}
            </div>

            {(decision === "REFUND" || decision === "RETURN_AND_REFUND") && (
              <div className={styles.formField}>
                <label htmlFor={feeId} className={shared.fieldLabel}>
                  Bên chịu phí hoàn hàng
                </label>
                <select
                  id={feeId}
                  className={shared.toolbarSelect}
                  value={feeBearer}
                  disabled={submitting}
                  onChange={(e) => setFeeBearer(e.target.value as ReturnFeeBearer)}
                >
                  {FEE_BEARER_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                <span className={styles.fieldHint}>
                  Lệnh hoàn tiền vượt ngưỡng tự động chuyển duyệt 2 người tại màn hình Đối soát.
                </span>
              </div>
            )}

            <div className={styles.formField}>
              <label htmlFor={noteId} className={shared.fieldLabel}>
                {decision === "REQUEST_EVIDENCE"
                  ? "Nội dung yêu cầu bổ sung bằng chứng (bắt buộc)"
                  : decision === "REJECT"
                    ? "Lý do từ chối khiếu nại (bắt buộc)"
                    : "Ghi chú phân xử (lưu nhật ký kiểm toán)"}
              </label>
              <textarea
                id={noteId}
                className={shared.textareaInput}
                value={note}
                disabled={submitting}
                onChange={(e) => {
                  setNote(e.target.value);
                  setFormError(null);
                }}
                placeholder={
                  decision === "REQUEST_EVIDENCE"
                    ? "Nêu rõ ảnh/video cần người mua bổ sung trong 3 ngày..."
                    : "Ghi rõ căn cứ bằng chứng và hướng xử lý..."
                }
              />
            </div>

            {formError && (
              <p className={shared.validationError} role="alert">
                {formError}
              </p>
            )}

            <div className={styles.formActions}>
              <Button type="submit" variant="primary" size="sm" shape="app" loading={submitting}>
                Xác nhận phân xử
              </Button>
            </div>
          </form>
        )}

        {resolvable && !canResolve && (
          <p className={styles.fieldHint}>Tài khoản của bạn chỉ có quyền xem hồ sơ khiếu nại.</p>
        )}
      </div>
    );
  };

  const tabs: Array<{ id: ReturnFilterTab; label: string; count?: number }> = [
    { id: "", label: "Tất cả", count: statusTab === "" ? total : undefined },
    { id: "ESCALATED", label: "Cần phân xử", count: escalatedCount },
    { id: "EVIDENCE_REQUESTED", label: "Chờ bằng chứng", count: evidenceCount },
    { id: "PENDING", label: "Chờ shop" },
    { id: "AWAITING_SHIPMENT", label: "Chờ gửi trả" },
    { id: "RECEIVED", label: "Shop đã nhận" },
    { id: "REFUNDING", label: "Đang hoàn tiền" },
    { id: "REFUNDED", label: "Đã hoàn tiền" },
    { id: "CLOSED", label: "Đã đóng" },
  ];

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Khiếu nại &amp; trả hàng</h1>
        </div>
        <div className={shared.titleActions}>
          <span className={shared.kbdHint} aria-label="Phím tắt chuyển hồ sơ">
            <kbd className={shared.kbdKey}>J</kbd>
            <kbd className={shared.kbdKey}>K</kbd>
            <span>chuyển hồ sơ</span>
          </span>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={statusTab}
        onChange={handleTabChange}
        ariaLabel="Lọc trạng thái yêu cầu trả hàng"
      />

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
              placeholder="Tìm mã đơn, mã yêu cầu, vận đơn, lý do..."
              aria-label="Tìm kiếm hồ sơ khiếu nại và trả hàng"
            />
          </div>
        </div>
        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(total)} hồ sơ</span>
          <Button
            variant="outline"
            size="sm"
            shape="app"
            disabled={loading}
            onClick={() => void loadList(statusTab, page)}
          >
            Làm mới
          </Button>
        </div>
      </div>

      {loadError ? (
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách khiếu nại"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => void loadList(statusTab, page)}
        />
      ) : loading ? (
        <div className={shared.stateWrap}>
          <StateView
            kind="loading"
            surface="app"
            title="Đang tải danh sách khiếu nại..."
            rows={5}
          />
        </div>
      ) : filteredItems.length === 0 ? (
        <p className={shared.emptyRow}>Không có yêu cầu trả hàng hoặc khiếu nại phù hợp.</p>
      ) : (
        <>
          <div className={shared.reviewSplit}>
            <div className={shared.queueColumn}>
              <div className={shared.queueHead}>
                <span>Danh sách yêu cầu</span>
                <span>
                  {activeIndex + 1} / {formatInt(filteredItems.length)}
                </span>
              </div>
              <ul className={shared.queueList} aria-label="Danh sách khiếu nại trả hàng">
                {filteredItems.map((item) => {
                  const meta = getAdminReturnStatusMeta(item.status);
                  const isSelected = activeReturn?.id === item.id;
                  return (
                    <li key={item.id} className={shared.queueItem}>
                      <button
                        ref={(node) => {
                          if (node) rowRefs.current.set(item.id, node);
                          else rowRefs.current.delete(item.id);
                        }}
                        type="button"
                        className={`${shared.queueItemBtn} ${isSelected ? shared.queueItemActive : ""}`}
                        aria-current={isSelected ? "true" : undefined}
                        onClick={() => {
                          setSelectedId(item.id);
                          if (typeof window !== "undefined" && window.innerWidth <= 960) {
                            setDrawerOpen(true);
                          }
                        }}
                      >
                        <span className={shared.queueItemBody}>
                          <span className={shared.queueItemTop}>
                            <span className={shared.queueItemTitle}>
                              Đơn {item.order_code || item.order_id.slice(0, 8)}
                            </span>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </span>
                          <span className={shared.queueItemMeta}>
                            {item.refund_amount_vnd > 0
                              ? formatVnd(item.refund_amount_vnd)
                              : EMPTY_DASH}
                            {item.created_at ? ` · ${formatShortTimeDateVi(item.created_at)}` : ""}
                          </span>
                          <span className={shared.queueItemMeta}>
                            {item.dispute_reason || item.reason || "Chưa có lý do"}
                          </span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </div>

            {activeReturn && (
              <section className={shared.inspectorPane} aria-label="Chi tiết phân xử khiếu nại">
                {renderInspector(activeReturn, false)}
              </section>
            )}
          </div>

          {totalPages > 1 && (
            <div className={styles.paginationRow}>
              <span className={styles.paginationInfo}>
                Trang {formatInt(page)} / {formatInt(totalPages)} ({formatInt(total)} hồ sơ)
              </span>
              <div className={styles.paginationBtns}>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={page <= 1 || loading}
                  onClick={() => handlePageChange(page - 1)}
                >
                  Trang trước
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  shape="app"
                  disabled={page >= totalPages || loading}
                  onClick={() => handlePageChange(page + 1)}
                >
                  Trang sau
                </Button>
              </div>
            </div>
          )}
        </>
      )}

      <AdminDrawer
        open={drawerOpen && Boolean(activeReturn)}
        onClose={() => setDrawerOpen(false)}
        title={
          activeReturn ? `Đơn ${activeReturn.order_code || activeReturn.order_id.slice(0, 8)}` : ""
        }
        subtitle={activeReturn?.created_at ? formatDateTimeVi(activeReturn.created_at) : undefined}
        headerBadge={
          activeReturn ? (
            <StatusPill
              label={getAdminReturnStatusMeta(activeReturn.status).label}
              tone={getAdminReturnStatusMeta(activeReturn.status).tone}
            />
          ) : undefined
        }
        width="lg"
      >
        {activeReturn && renderInspector(activeReturn, true)}
      </AdminDrawer>
    </div>
  );
}

export function AdminReturnsClient(props: AdminReturnsClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Khiếu nại & trả hàng" />}>
      <AdminStateGate
        title="Khiếu nại & trả hàng"
        emptyTitle="Chưa có yêu cầu trả hàng hoặc khiếu nại"
        emptyMessage="Hồ sơ tranh chấp từ người mua sẽ xuất hiện tại đây."
        errorTitle="Không thể tải danh sách khiếu nại"
        errorMessage="Dịch vụ đơn hàng tạm gián đoạn. Vui lòng thử lại."
        loadingRows={5}
      >
        {({ initialQuery, initialSelected }) => (
          <ReturnsWorkspace
            {...props}
            initialQuery={initialQuery}
            initialSelected={initialSelected}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
