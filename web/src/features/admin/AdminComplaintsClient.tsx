"use client";

import React, { Suspense, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, IconSearch, StateView } from "@/components";
import { api, ApiError } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { sanitizeSafeUrl } from "@/lib/site/theme";
import { EMPTY_DASH, formatShortTimeDateVi } from "./adminMeta";
import { AdminStateGate, AdminSuspenseFallback } from "./AdminStateGate";
import { useAdminContext } from "./AdminShell";
import { AdminDrawer, SlidingTabBar } from "./AdminUi";
import { StatusPill } from "./StatusPill";
import {
  getComplaintResolutionLabelVi,
  getComplaintStatusMeta,
  isComplaintResolved,
  normalizeComplaint,
  validateComplaintResolution,
  type AdminComplaint,
  type ComplaintResolution,
} from "./adminComplaintsTypes";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/khieu-nai/page.module.css";

type ComplaintFilterTab = "all" | "OPEN" | "RESOLVED" | "DISMISSED";

const RESOLUTION_OPTIONS: Array<{
  value: ComplaintResolution;
  title: string;
  hint: string;
}> = [
  {
    value: "REFUND",
    title: "Hoàn tiền cho người mua",
    hint: "Hoàn toàn bộ giá trị đơn hàng (ngay hoặc sau khi nhận hàng hoàn).",
  },
  {
    value: "REPLACE",
    title: "Đổi sản phẩm thay thế",
    hint: "Tạo vận đơn chiều ngược để người mua gửi trả và nhận hàng thay thế.",
  },
  {
    value: "DISMISS",
    title: "Bác khiếu nại",
    hint: "Giữ nguyên đơn hoàn tất và giải ngân cho gian hàng.",
  },
];

export interface AdminComplaintsClientProps {
  initialComplaints: AdminComplaint[];
  loadError: string | null;
}

function bucketStatus(status: string): "OPEN" | "RESOLVED" | "DISMISSED" {
  if (status === "DISMISSED") return "DISMISSED";
  if (status === "RESOLVED") return "RESOLVED";
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
  const [selectedId, setSelectedId] = useState<string | null>(initialSelected);
  const [drawerOpen, setDrawerOpen] = useState(Boolean(initialSelected));

  const [resolution, setResolution] = useState<ComplaintResolution>("REFUND");
  const [requireReturnShipping, setRequireReturnShipping] = useState(false);
  const [note, setNote] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const counts = useMemo(() => {
    const map: Record<ComplaintFilterTab, number> = {
      all: complaints.length,
      OPEN: 0,
      RESOLVED: 0,
      DISMISSED: 0,
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
    if (!selectedId) return null;
    return complaints.find((c) => c.id === selectedId) ?? null;
  }, [complaints, selectedId]);

  useEffect(() => {
    setFormError(null);
    setNote("");
    setResolution("REFUND");
    setRequireReturnShipping(false);
  }, [activeComplaint?.id]);

  if (loadError) {
    return (
      <div className={shared.page}>
        <header className={shared.titleRow}>
          <div className={shared.titleBlock}>
            <h1 className={shared.pageTitle}>Khiếu nại</h1>
          </div>
        </header>
        <StateView
          kind="error"
          surface="app"
          title="Không thể tải danh sách khiếu nại"
          message={loadError}
          actionLabel="Thử lại"
          onAction={() => router.refresh()}
        />
      </div>
    );
  }

  const handleOpenComplaint = (item: AdminComplaint) => {
    setSelectedId(item.id);
    setDrawerOpen(true);
  };

  const handleResolveSubmit = async (e: React.FormEvent, target: AdminComplaint) => {
    e.preventDefault();
    if (submitting) return;

    const effectiveReturnShipping =
      resolution === "REPLACE" ? true : resolution === "REFUND" ? requireReturnShipping : false;

    const validated = validateComplaintResolution({
      resolution,
      note,
      require_return_shipping: effectiveReturnShipping,
    });
    if (!validated.ok) {
      setFormError(validated.message);
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

      const resolvedStatus = resolution === "DISMISS" ? "DISMISSED" : "RESOLVED";
      const updatedComplaint: AdminComplaint = res.complaint
        ? normalizeComplaint(res.complaint)
        : {
            ...target,
            status: resolvedStatus,
            resolution: validated.value.resolution,
            resolution_note: validated.value.note,
            resolved_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          };

      setComplaints((prev) =>
        prev.map((item) => (item.id === target.id ? updatedComplaint : item))
      );
      setNote("");
      setRequireReturnShipping(false);
      showToast(
        `Đã phân xử ${target.complaint_code}: ${getComplaintResolutionLabelVi(resolution)}.`
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

  const renderDrawerBody = (complaint: AdminComplaint) => {
    const resolved = isComplaintResolved(complaint.status);
    const safePhotos = complaint.evidence_urls
      .map((u) => sanitizeSafeUrl(u))
      .filter((u) => u.length > 0);

    return (
      <>
        <div className={shared.detailWell}>
          <div className={shared.detailGrid}>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Giá trị tranh chấp</span>
              <span className={`${shared.detailVal} num`}>
                {complaint.dispute_amount_vnd > 0
                  ? formatVnd(complaint.dispute_amount_vnd)
                  : EMPTY_DASH}
              </span>
            </div>
            <div className={shared.detailField}>
              <span className={shared.detailKey}>Mã đơn hàng</span>
              <span className={`${shared.detailVal} num`}>
                {complaint.order_code || EMPTY_DASH}
              </span>
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
        </div>

        <div className={styles.reasonBlock}>
          <div className={styles.statementBox}>
            <span className={styles.statementTitle}>Lý do khiếu nại</span>
            <p className={styles.statementBody}>
              {complaint.buyer_reason || "Chưa có mô tả chi tiết."}
            </p>
          </div>

          {complaint.seller_rejection_reason && (
            <div className={styles.statementBox}>
              <span className={styles.statementTitle}>Phản hồi từ chối của gian hàng</span>
              <p className={styles.statementBody}>{complaint.seller_rejection_reason}</p>
            </div>
          )}
        </div>

        <div className={styles.photoSection}>
          <span className={shared.fieldLabel}>Ảnh bằng chứng ({formatInt(safePhotos.length)})</span>
          {safePhotos.length === 0 ? (
            <p className={styles.fieldHint}>Chưa có ảnh bằng chứng đính kèm.</p>
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
                      {ev.actor_name || ev.actor_role}: {ev.action}
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
              Kết quả phân xử: {getComplaintResolutionLabelVi(complaint.resolution)}
            </span>
            <p className={styles.resolutionText}>
              Ghi chú quản trị: {complaint.resolution_note || EMPTY_DASH}
            </p>
            {complaint.resolved_at && (
              <span className={styles.fieldHint}>
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
              {RESOLUTION_OPTIONS.map((opt) => {
                const checked = resolution === opt.value;
                return (
                  <label
                    key={opt.value}
                    className={`${styles.radioOption} ${checked ? styles.radioOptionSelected : ""}`}
                  >
                    <input
                      type="radio"
                      name={`complaint-resolution-${complaint.id}`}
                      value={opt.value}
                      checked={checked}
                      onChange={() => {
                        setResolution(opt.value);
                        if (opt.value !== "REFUND") {
                          setRequireReturnShipping(opt.value === "REPLACE");
                        }
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

            {resolution === "REFUND" && (
              <label className={styles.checkboxOption}>
                <input
                  type="checkbox"
                  checked={requireReturnShipping}
                  onChange={(e) => {
                    setRequireReturnShipping(e.target.checked);
                    setFormError(null);
                  }}
                  className={styles.checkboxInput}
                />
                <span className={styles.radioText}>
                  <span className={styles.radioTitle}>
                    Yêu cầu người mua gửi trả hàng trước khi hoàn tiền
                  </span>
                  <span className={styles.radioHint}>
                    Bật để tạo vận đơn hoàn hàng trước; bỏ chọn để hoàn tiền ngay.
                  </span>
                </span>
              </label>
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
                placeholder="Ghi rõ căn cứ bằng chứng và hướng xử lý để lưu kiểm toán..."
              />
            </div>

            {formError && <p className={shared.validationError}>{formError}</p>}

            <div className={styles.formActions}>
              <Button type="submit" variant="primary" size="sm" shape="app" loading={submitting}>
                Xác nhận phân xử
              </Button>
            </div>
          </form>
        )}
      </>
    );
  };

  const tabs: Array<{ id: ComplaintFilterTab; label: string; count: number }> = [
    { id: "all", label: "Tất cả", count: counts.all },
    { id: "OPEN", label: "Chờ xử lý", count: counts.OPEN },
    { id: "RESOLVED", label: "Đã giải quyết", count: counts.RESOLVED },
    { id: "DISMISSED", label: "Đã bác", count: counts.DISMISSED },
  ];

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Khiếu nại</h1>
        </div>
      </header>

      <SlidingTabBar
        tabs={tabs}
        activeId={activeTab}
        onChange={setActiveTab}
        ariaLabel="Lọc trạng thái khiếu nại"
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
        <StateView
          kind="empty"
          surface="app"
          title="Chưa có khiếu nại cần phân xử"
          message="Hồ sơ tranh chấp từ người mua sẽ xuất hiện tại đây."
        />
      ) : filtered.length === 0 ? (
        <p className={shared.emptyRow}>Không tìm thấy hồ sơ khiếu nại phù hợp.</p>
      ) : (
        <>
          <div className={shared.tableWrap}>
            <table className={shared.dataTable}>
              <thead>
                <tr>
                  <th>Khiếu nại &amp; đơn hàng</th>
                  <th>Người mua &amp; gian hàng</th>
                  <th className={shared.colNum}>Tranh chấp</th>
                  <th>Trạng thái</th>
                  <th className={shared.colAction}>Thao tác</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((item) => {
                  const meta = getComplaintStatusMeta(item.status);
                  const isSelected = drawerOpen && activeComplaint?.id === item.id;
                  return (
                    <tr
                      key={item.id}
                      className={`${shared.rowInteractive} ${isSelected ? shared.rowSelected : ""}`}
                      onClick={() => handleOpenComplaint(item)}
                    >
                      <td>
                        <div className={shared.cellPrimary}>
                          {item.complaint_code || item.id.slice(0, 12)}
                        </div>
                        <div className={shared.cellSub}>
                          Đơn {item.order_code || EMPTY_DASH}
                          {item.created_at ? `, ${formatShortTimeDateVi(item.created_at)}` : ""}
                        </div>
                      </td>
                      <td>
                        <div className={shared.cellPrimary}>{item.buyer_name || EMPTY_DASH}</div>
                        <div className={shared.cellSub}>Shop: {item.shop_name || EMPTY_DASH}</div>
                      </td>
                      <td className={shared.colNum}>
                        {item.dispute_amount_vnd > 0
                          ? formatVnd(item.dispute_amount_vnd)
                          : EMPTY_DASH}
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
                            handleOpenComplaint(item);
                          }}
                        >
                          {isComplaintResolved(item.status) ? "Xem hồ sơ" : "Phân xử"}
                        </button>
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
                    onClick={() => handleOpenComplaint(item)}
                  >
                    <span className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>
                        {item.complaint_code || item.id.slice(0, 12)} ({item.order_code})
                      </span>
                      <span className={shared.mobileMetric}>
                        {item.dispute_amount_vnd > 0
                          ? formatVnd(item.dispute_amount_vnd)
                          : EMPTY_DASH}
                      </span>
                    </span>
                    <span className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {[item.buyer_name, item.shop_name].filter(Boolean).join(", ") || EMPTY_DASH}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </span>
                  </button>
                </div>
              );
            })}
          </div>
        </>
      )}

      {activeComplaint && (
        <AdminDrawer
          open={drawerOpen}
          onClose={() => setDrawerOpen(false)}
          title={activeComplaint.complaint_code || activeComplaint.id}
          subtitle={`Đơn ${activeComplaint.order_code || EMPTY_DASH}${activeComplaint.created_at ? `, ${formatDateTimeVi(activeComplaint.created_at)}` : ""}`}
          headerBadge={
            <StatusPill
              label={getComplaintStatusMeta(activeComplaint.status).label}
              tone={getComplaintStatusMeta(activeComplaint.status).tone}
            />
          }
          width="lg"
        >
          {renderDrawerBody(activeComplaint)}
        </AdminDrawer>
      )}
    </div>
  );
}

export function AdminComplaintsClient(props: AdminComplaintsClientProps) {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Khiếu nại" />}>
      <AdminStateGate
        title="Khiếu nại"
        emptyTitle="Chưa có khiếu nại cần phân xử"
        emptyMessage="Tất cả hồ sơ tranh chấp đã được giải quyết."
        errorTitle="Không thể tải danh sách khiếu nại"
        errorMessage="Dịch vụ khiếu nại tạm gián đoạn. Vui lòng thử lại."
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
