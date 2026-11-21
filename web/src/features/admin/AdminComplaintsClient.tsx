"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconClose, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { StatusPill } from "./StatusPill";
import {
  getComplaintDecisionLabelVi,
  getComplaintStatusMeta,
  isComplaintResolved,
  normalizeComplaint,
  validateComplaintResolution,
  type AdminComplaint,
  type ComplaintDecision,
} from "./adminComplaintsTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/khieu-nai/page.module.css";

type ComplaintFilterTab = "all" | "OPEN" | "IN_REVIEW" | "RESOLVED";

const DECISION_OPTIONS: Array<{
  value: ComplaintDecision;
  title: string;
  hint: string;
}> = [
  {
    value: "REFUND_BUYER",
    title: "Hoàn toàn bộ tiền cho người mua",
    hint: "Tạo lệnh hoàn toàn bộ giá trị tranh chấp về cho người mua và đóng khiếu nại.",
  },
  {
    value: "APPROVE_RETURN",
    title: "Chấp thuận trả hàng",
    hint: "Buộc gian hàng tiếp nhận hàng hoàn trả theo vận đơn chiều ngược.",
  },
  {
    value: "PARTIAL_REFUND",
    title: "Hoàn một phần tiền",
    hint: "Người mua giữ sản phẩm và nhận hoàn số tiền VND thỏa thuận dưới đây.",
  },
  {
    value: "REJECT_COMPLAINT",
    title: "Bác khiếu nại",
    hint: "Giữ nguyên giao dịch cho gian hàng khi bằng chứng khiếu nại không thuyết phục.",
  },
];

export interface AdminComplaintsClientProps {
  initialComplaints: AdminComplaint[];
  loadError: string | null;
}

function bucketStatus(status: string): "OPEN" | "IN_REVIEW" | "RESOLVED" {
  if (status === "IN_REVIEW") return "IN_REVIEW";
  if (isComplaintResolved(status)) return "RESOLVED";
  return "OPEN";
}

function ComplaintsWorkspace({
  initialComplaints,
  loadError,
  initialQuery,
  initialSelected,
}: {
  initialComplaints: AdminComplaint[];
  loadError: string | null;
  initialQuery: string;
  initialSelected: string | null;
}) {
  const router = useRouter();
  const { showToast } = useAdminContext();

  const [complaints, setComplaints] = useState<AdminComplaint[]>(initialComplaints);
  const [activeTab, setActiveTab] = useState<ComplaintFilterTab>("all");
  const [query, setQuery] = useState(initialQuery);
  const [selectedId, setSelectedId] = useState<string | null>(
    initialSelected ?? initialComplaints[0]?.id ?? null
  );
  const [drawerOpen, setDrawerOpen] = useState(Boolean(initialSelected));

  const [decision, setDecision] = useState<ComplaintDecision>("REFUND_BUYER");
  const [partialAmountInput, setPartialAmountInput] = useState("");
  const [note, setNote] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const counts = useMemo(() => {
    const map: Record<ComplaintFilterTab, number> = {
      all: complaints.length,
      OPEN: 0,
      IN_REVIEW: 0,
      RESOLVED: 0,
    };
    for (const item of complaints) {
      map[bucketStatus(item.status)] += 1;
    }
    return map;
  }, [complaints]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return complaints.filter((c) => {
      if (activeTab !== "all" && bucketStatus(c.status) !== activeTab) return false;
      if (!q) return true;
      return (
        c.id.toLowerCase().includes(q) ||
        c.complaint_code.toLowerCase().includes(q) ||
        c.order_code.toLowerCase().includes(q) ||
        c.order_id.toLowerCase().includes(q) ||
        c.shop_name.toLowerCase().includes(q) ||
        c.shop_id.toLowerCase().includes(q) ||
        c.buyer_name.toLowerCase().includes(q) ||
        c.buyer_id.toLowerCase().includes(q) ||
        c.buyer_reason.toLowerCase().includes(q) ||
        c.seller_rejection_reason.toLowerCase().includes(q)
      );
    });
  }, [complaints, activeTab, query]);

  const activeComplaint = useMemo(() => {
    if (filtered.length === 0) return null;
    if (selectedId) {
      const exact = filtered.find((c) => c.id === selectedId);
      if (exact) return exact;
    }
    return filtered[0];
  }, [filtered, selectedId]);

  useEffect(() => {
    setFormError(null);
    setNote("");
    setDecision("REFUND_BUYER");
    setPartialAmountInput("");
  }, [activeComplaint?.id]);

  useEffect(() => {
    if (!drawerOpen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setDrawerOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [drawerOpen]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Khiếu nại</h1>
            <p className={shared.pageSubtitle}>
              Phân xử tranh chấp trả hàng, hoàn tiền giữa người mua và gian hàng.
            </p>
          </div>
        </header>
        <div className={shared.stateWrap}>
          <StateView
            kind="error"
            surface="app"
            title="Không thể tải danh sách khiếu nại"
            message={loadError}
            actionLabel="Thử tải lại"
            onAction={() => router.refresh()}
          />
        </div>
      </div>
    );
  }

  const handleSelectComplaint = (item: AdminComplaint, openMobileDrawer = false) => {
    setSelectedId(item.id);
    if (openMobileDrawer) {
      setDrawerOpen(true);
    }
  };

  const handleResolveSubmit = async (e: React.FormEvent, target: AdminComplaint) => {
    e.preventDefault();
    if (submitting) return;

    const rawRefund =
      decision === "PARTIAL_REFUND"
        ? partialAmountInput.trim() === ""
          ? Number.NaN
          : Number(partialAmountInput.trim())
        : undefined;

    const validated = validateComplaintResolution({
      decision,
      refund_amount_vnd: rawRefund,
      note,
    });
    if (!validated.ok) {
      setFormError(validated.message);
      return;
    }

    if (
      decision === "PARTIAL_REFUND" &&
      target.dispute_amount_vnd > 0 &&
      (validated.value.refund_amount_vnd ?? 0) > target.dispute_amount_vnd
    ) {
      setFormError(
        `Số tiền hoàn một phần không được vượt quá giá trị tranh chấp (${formatVnd(target.dispute_amount_vnd)}).`
      );
      return;
    }

    setFormError(null);
    setSubmitting(true);
    try {
      const res = await api<{ complaint?: unknown }>(
        `/api/admin/complaints/${encodeURIComponent(target.id)}/resolve`,
        {
          method: "POST",
          body: validated.value,
        }
      );

      const resolvedStatus = decision === "REJECT_COMPLAINT" ? "DISMISSED" : "RESOLVED";
      const updatedComplaint: AdminComplaint = res.complaint
        ? normalizeComplaint(res.complaint)
        : {
            ...target,
            status: resolvedStatus,
            decision: validated.value.decision,
            refund_amount_vnd: validated.value.refund_amount_vnd ?? target.refund_amount_vnd,
            resolution_note: validated.value.note,
            resolved_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          };

      setComplaints((prev) =>
        prev.map((item) => (item.id === target.id ? updatedComplaint : item))
      );
      setNote("");
      setPartialAmountInput("");
      showToast(
        `Đã phân xử khiếu nại ${target.complaint_code}: ${getComplaintDecisionLabelVi(decision)}.`
      );
      router.refresh();
    } catch (err) {
      const msg =
        err instanceof ApiError ? err.message : "Không thể gửi quyết định phân xử lúc này.";
      setFormError(msg);
      showToast(msg);
    } finally {
      setSubmitting(false);
    }
  };

  const renderInspectorBody = (complaint: AdminComplaint) => {
    const meta = getComplaintStatusMeta(complaint.status);
    const resolved = isComplaintResolved(complaint.status);
    const safePhotos = complaint.evidence_urls
      .map((u) => sanitizeSafeUrl(u))
      .filter((u) => u.length > 0);
    const parsedPartialPreview =
      partialAmountInput.trim() !== "" && Number.isInteger(Number(partialAmountInput.trim()))
        ? Number(partialAmountInput.trim())
        : null;

    return (
      <div className={styles.inspectorCard}>
        <div className={styles.inspectorTop}>
          <div className={styles.inspectorTitleGroup}>
            <h2 className={styles.inspectorTitle}>{complaint.complaint_code || complaint.id}</h2>
            <span className={styles.inspectorMetaLine}>
              Đơn hàng {complaint.order_code || EMPTY_DASH} · Tạo lúc{" "}
              {complaint.created_at ? formatDateTimeVi(complaint.created_at) : EMPTY_DASH}
            </span>
          </div>
          <StatusPill label={meta.label} tone={meta.tone} />
        </div>

        <div className={styles.metaGrid}>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Giá trị tranh chấp</span>
            <span className={shared.detailVal}>{formatVnd(complaint.dispute_amount_vnd)}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Mã đơn hàng</span>
            <span className={shared.detailVal}>{complaint.order_code || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Người mua</span>
            <span className={shared.detailValPlain}>{complaint.buyer_name || EMPTY_DASH}</span>
          </div>
          <div className={shared.detailField}>
            <span className={shared.detailKey}>Gian hàng</span>
            <span className={shared.detailValPlain}>{complaint.shop_name || EMPTY_DASH}</span>
          </div>
        </div>

        <div className={styles.reasonBlock}>
          <div className={styles.statementBox}>
            <span className={styles.statementTitle}>Lý do trả hàng từ người mua</span>
            <p className={styles.statementBody}>
              {complaint.buyer_reason || "Người mua chưa cung cấp mô tả chi tiết."}
            </p>
          </div>

          <div className={styles.statementBox}>
            <span className={styles.statementTitle}>Phản hồi từ chối của gian hàng</span>
            <p className={styles.statementBody}>
              {complaint.seller_rejection_reason || "Gian hàng chưa ghi nhận lý do từ chối cụ thể."}
            </p>
          </div>
        </div>

        <div className={styles.photoSection}>
          <span className={shared.fieldLabel}>Ảnh bằng chứng ({formatInt(safePhotos.length)})</span>
          {safePhotos.length === 0 ? (
            <p className={styles.fieldHint}>Chưa có ảnh bằng chứng hợp lệ đính kèm hồ sơ này.</p>
          ) : (
            <div className={styles.photoGrid}>
              {safePhotos.map((url, idx) => (
                <a
                  key={`${url}-${idx}`}
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={styles.photoLink}
                  aria-label={`Mở ảnh bằng chứng số ${idx + 1} trong thẻ mới`}
                >
                  <img
                    src={url}
                    alt={`Bằng chứng khiếu nại ${complaint.complaint_code} ảnh ${idx + 1}`}
                    className={styles.photoImg}
                  />
                </a>
              ))}
            </div>
          )}
        </div>

        {complaint.timeline.length > 0 && (
          <div className={styles.photoSection}>
            <span className={shared.fieldLabel}>Diễn biến &amp; ghi chú</span>
            <ul className={styles.timelineList}>
              {complaint.timeline.map((ev) => (
                <li key={ev.id} className={styles.timelineItem}>
                  <div className={styles.timelineHead}>
                    <span className={styles.timelineActor}>
                      {ev.actor_name || ev.actor_role} · {ev.action}
                    </span>
                    <span className={styles.timelineTime}>
                      {ev.created_at ? formatShortTimeDateVi(ev.created_at) : EMPTY_DASH}
                    </span>
                  </div>
                  {ev.note && <p className={styles.timelineNote}>{ev.note}</p>}
                </li>
              ))}
            </ul>
          </div>
        )}

        {resolved ? (
          <div className={styles.resolutionSummary}>
            <span className={styles.resolutionHeading}>
              Kết quả phân xử: {getComplaintDecisionLabelVi(complaint.decision)}
            </span>
            {complaint.refund_amount_vnd !== null && (
              <p className={styles.resolutionText}>
                Số tiền hoàn đã duyệt: {formatVnd(complaint.refund_amount_vnd)}
              </p>
            )}
            <p className={styles.resolutionText}>
              Ghi chú quản trị: {complaint.resolution_note || EMPTY_DASH}
            </p>
            {complaint.resolved_at && (
              <span className={styles.inspectorMetaLine}>
                Hoàn tất lúc {formatDateTimeVi(complaint.resolved_at)}
              </span>
            )}
          </div>
        ) : (
          <form
            className={styles.decisionForm}
            onSubmit={(e) => void handleResolveSubmit(e, complaint)}
            noValidate
          >
            <h3 className={styles.decisionHeading}>Quyết định phân xử</h3>

            <div
              className={styles.radioGroup}
              role="radiogroup"
              aria-label="Chọn hướng phân xử khiếu nại"
            >
              {DECISION_OPTIONS.map((opt) => {
                const checked = decision === opt.value;
                return (
                  <label
                    key={opt.value}
                    className={`${styles.radioOption} ${checked ? styles.radioOptionSelected : ""}`}
                  >
                    <input
                      type="radio"
                      name={`complaint-decision-${complaint.id}`}
                      value={opt.value}
                      checked={checked}
                      onChange={() => {
                        setDecision(opt.value);
                        setFormError(null);
                      }}
                      className={styles.radioInput}
                    />
                    <span className={styles.radioText}>
                      <span className={styles.radioTitle}>{opt.title}</span>
                      <span className={styles.radioHint}>{opt.hint}</span>
                    </span>
                  </label>
                );
              })}
            </div>

            {decision === "PARTIAL_REFUND" && (
              <div className={styles.formField}>
                <label htmlFor={`partial-refund-${complaint.id}`} className={shared.fieldLabel}>
                  Số tiền hoàn một phần (số nguyên VND)
                </label>
                <input
                  id={`partial-refund-${complaint.id}`}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  step={1}
                  className={shared.textInput}
                  value={partialAmountInput}
                  onChange={(e) => {
                    setPartialAmountInput(e.target.value);
                    setFormError(null);
                  }}
                  placeholder="Ví dụ: 150000"
                />
                <span className={styles.fieldHint}>
                  {parsedPartialPreview !== null && parsedPartialPreview >= 0
                    ? `Sẽ hoàn ${formatVnd(parsedPartialPreview)} cho người mua.`
                    : `Nhập số nguyên VND không âm (tối đa ${formatVnd(complaint.dispute_amount_vnd)}).`}
                </span>
              </div>
            )}

            <div className={styles.formField}>
              <label htmlFor={`resolve-note-${complaint.id}`} className={shared.fieldLabel}>
                Ghi chú phân xử (bắt buộc, tối thiểu 5 ký tự)
              </label>
              <textarea
                id={`resolve-note-${complaint.id}`}
                className={shared.textareaInput}
                value={note}
                onChange={(e) => {
                  setNote(e.target.value);
                  setFormError(null);
                }}
                placeholder="Ghi rõ căn cứ bằng chứng và hướng xử lý để lưu vào nhật ký kiểm toán..."
              />
            </div>

            {formError && <p className={shared.validationError}>{formError}</p>}

            <div className={styles.formActions}>
              <Button type="submit" variant="primary" size="sm" shape="app" disabled={submitting}>
                {submitting ? "Đang lưu quyết định..." : "Xác nhận phân xử"}
              </Button>
            </div>
          </form>
        )}
      </div>
    );
  };

  const tabs: Array<{ id: ComplaintFilterTab; label: string }> = [
    { id: "all", label: "Tất cả" },
    { id: "OPEN", label: "Chờ xử lý" },
    { id: "IN_REVIEW", label: "Đang xem xét" },
    { id: "RESOLVED", label: "Đã giải quyết" },
  ];

  const subtitle =
    counts.OPEN + counts.IN_REVIEW > 0
      ? `${formatInt(counts.OPEN)} khiếu nại chờ xử lý, ${formatInt(counts.IN_REVIEW)} đang xem xét và ${formatInt(counts.RESOLVED)} đã hoàn tất phân xử.`
      : `Không có khiếu nại tồn đọng; đã giải quyết ${formatInt(counts.RESOLVED)} hồ sơ tranh chấp.`;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Khiếu nại</h1>
          <p className={shared.pageSubtitle}>{subtitle}</p>
        </div>
      </header>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Lọc trạng thái khiếu nại">
          {tabs.map((tab) => {
            const active = activeTab === tab.id;
            return (
              <button
                key={tab.id}
                type="button"
                role="tab"
                aria-selected={active}
                className={`${shared.tabBtn} ${active ? shared.tabBtnActive : ""}`}
                onClick={() => setActiveTab(tab.id)}
              >
                <span>{tab.label}</span>
                <span className={shared.tabCount}>{counts[tab.id]}</span>
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
              placeholder="Tìm mã khiếu nại, mã đơn, gian hàng, người mua..."
              aria-label="Tìm kiếm hồ sơ khiếu nại"
            />
          </div>
        </div>
        <div className={shared.toolbarRight}>
          <span className={shared.resultCount}>{formatInt(filtered.length)} hồ sơ</span>
        </div>
      </div>

      {complaints.length === 0 ? (
        <div className={shared.stateWrap}>
          <StateView
            kind="empty"
            surface="app"
            title="Chưa có khiếu nại nào cần phân xử"
            message="Khi người mua khiếu nại quyết định từ chối trả hàng từ gian hàng, hồ sơ sẽ xuất hiện tại đây."
          />
        </div>
      ) : filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không tìm thấy hồ sơ khiếu nại phù hợp với bộ lọc.</p>
      ) : (
        <>
          <div className={styles.splitGrid}>
            <div className={styles.masterPane}>
              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Khiếu nại &amp; đơn hàng</th>
                      <th>Người mua &amp; gian hàng</th>
                      <th className={shared.colNum}>Tranh chấp</th>
                      <th>Trạng thái</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map((item) => {
                      const meta = getComplaintStatusMeta(item.status);
                      const isSelected = activeComplaint?.id === item.id;
                      return (
                        <tr
                          key={item.id}
                          className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                          onClick={() => handleSelectComplaint(item, true)}
                        >
                          <td>
                            <div className={shared.cellPrimary}>
                              {item.complaint_code || item.id.slice(0, 12)}
                            </div>
                            <div className={shared.cellSub}>
                              Đơn {item.order_code || EMPTY_DASH} ·{" "}
                              {item.created_at
                                ? formatShortTimeDateVi(item.created_at)
                                : EMPTY_DASH}
                            </div>
                          </td>
                          <td>
                            <div className={shared.cellPrimary}>
                              {item.buyer_name || EMPTY_DASH}
                            </div>
                            <div className={shared.cellSub}>
                              Shop: {item.shop_name || EMPTY_DASH}
                            </div>
                          </td>
                          <td className={shared.colNum}>{formatVnd(item.dispute_amount_vnd)}</td>
                          <td>
                            <StatusPill label={meta.label} tone={meta.tone} />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {filtered.map((item) => {
                  const meta = getComplaintStatusMeta(item.status);
                  return (
                    <div key={item.id} className={shared.mobileItem}>
                      <button
                        type="button"
                        className={shared.mobileItemBtn}
                        onClick={() => handleSelectComplaint(item, true)}
                      >
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>
                            {item.complaint_code || item.id.slice(0, 12)} · {item.order_code}
                          </span>
                          <span className={shared.mobileMetric}>
                            {formatVnd(item.dispute_amount_vnd)}
                          </span>
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {item.buyer_name} · {item.shop_name}
                          </span>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </div>
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>

            {activeComplaint && (
              <aside
                className={styles.inspectorDesktop}
                aria-label="Chi tiết hồ sơ khiếu nại và phân xử"
              >
                {renderInspectorBody(activeComplaint)}
              </aside>
            )}
          </div>

          {drawerOpen && activeComplaint && (
            <>
              <div
                className={styles.mobileDrawerBackdrop}
                onClick={() => setDrawerOpen(false)}
                aria-hidden="true"
              />
              <div
                className={styles.mobileDrawer}
                role="dialog"
                aria-modal="true"
                aria-label={`Chi tiết khiếu nại ${activeComplaint.complaint_code}`}
              >
                <div className={styles.mobileDrawerHeader}>
                  <span className={styles.inspectorTitle}>
                    Hồ sơ {activeComplaint.complaint_code}
                  </span>
                  <button
                    type="button"
                    className={styles.drawerCloseBtn}
                    onClick={() => setDrawerOpen(false)}
                    aria-label="Đóng chi tiết khiếu nại"
                  >
                    <IconClose size={18} />
                  </button>
                </div>
                <div className={styles.mobileDrawerBody}>
                  {renderInspectorBody(activeComplaint)}
                </div>
              </div>
            </>
          )}
        </>
      )}
    </div>
  );
}

export function AdminComplaintsClient(props: AdminComplaintsClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Khiếu nại" />}>
      <AdminStateGate
        title="Khiếu nại"
        subtitle="Phân xử tranh chấp trả hàng, hoàn tiền giữa người mua và gian hàng."
        emptyTitle="Chưa có khiếu nại nào cần phân xử"
        emptyMessage="Tất cả hồ sơ tranh chấp đã được giải quyết."
        errorTitle="Không thể tải danh sách khiếu nại"
        errorMessage="Dịch vụ khiếu nại tạm thời không phản hồi. Vui lòng thử tải lại."
        loadingRows={5}
      >
        {({ initialQuery, initialSelected }) => (
          <ComplaintsWorkspace
            initialComplaints={props.initialComplaints}
            loadError={props.loadError}
            initialQuery={initialQuery}
            initialSelected={initialSelected}
          />
        )}
      </AdminStateGate>
    </Suspense>
  );
}
