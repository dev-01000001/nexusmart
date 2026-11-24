"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import type {
  GatewayBannedKeyword,
  GatewayBannedKeywordAction,
  GatewayPreviewBannedKeywordResponse,
  GatewayRescanProductsResponse,
  PaginatedResponse,
} from "@/lib/gateway-types";
import { api, getErrorMessage } from "@/lib/api";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button, IconSearch, StateView } from "@/components";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import {
  type RescanAggregateProgress,
  RescanBatchInterruptedError,
  runBannedKeywordRescanBatches,
} from "./bannedKeywordsHelper";
import { getAdminProductStatusMeta } from "./types";
import { StatusPill } from "./StatusPill";
import { ToggleSwitch } from "./ToggleSwitch";
import shared from "./adminShared.module.css";

interface Props {
  onMutated?: () => void;
}

const KEYWORD_CATEGORIES: Array<{ value: string; label: string }> = [
  { value: "COUNTERFEIT", label: "Hàng giả / vi phạm bản quyền" },
  { value: "PROHIBITED", label: "Hàng cấm kinh doanh" },
  { value: "OFF_PLATFORM", label: "Dẫn giao dịch ngoài sàn" },
  { value: "SPAM", label: "Spam / nội dung vi phạm" },
  { value: "OTHER", label: "Nhóm khác" },
];

function formatCategoryLabel(category: string): string {
  const found = KEYWORD_CATEGORIES.find((c) => c.value === category);
  return found ? found.label : category || "Nhóm khác";
}

function formatActionPill(action: string): { label: string; tone: "negative" | "warning" } {
  if (action.toUpperCase() === "BLOCK") {
    return { label: "Chặn (BLOCK)", tone: "negative" };
  }
  return { label: "Gắn cờ duyệt (REVIEW)", tone: "warning" };
}

export function AdminBannedKeywordsTab({ onMutated }: Props) {
  const { showToast } = useMockStore();

  const [items, setItems] = useState<GatewayBannedKeyword[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [actionFilter, setActionFilter] = useState<"" | GatewayBannedKeywordAction>("");
  const [includeInactive, setIncludeInactive] = useState(true);

  const loadSeqRef = useRef(0);
  const loadAbortRef = useRef<AbortController | null>(null);

  // Create + preview form
  const [newTerm, setNewTerm] = useState("");
  const [newCategory, setNewCategory] = useState("COUNTERFEIT");
  const [newAction, setNewAction] = useState<GatewayBannedKeywordAction>("BLOCK");
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewResult, setPreviewResult] = useState<GatewayPreviewBannedKeywordResponse | null>(
    null
  );

  // Inline edit
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editTerm, setEditTerm] = useState("");
  const [editCategory, setEditCategory] = useState("COUNTERFEIT");
  const [editAction, setEditAction] = useState<GatewayBannedKeywordAction>("BLOCK");
  const [editActive, setEditActive] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);

  // Batch rescan (E11: limit 500 per batch, cursor loop, AbortController on stop/unmount)
  const [dryRun, setDryRun] = useState(true);
  const [rescanning, setRescanning] = useState(false);
  const [rescanStatus, setRescanStatus] = useState<
    "idle" | "running" | "completed" | "stopped" | "interrupted"
  >("idle");
  const [rescanFailedBatch, setRescanFailedBatch] = useState<number | null>(null);
  const [rescanError, setRescanError] = useState<string | null>(null);
  const [rescanProgress, setRescanProgress] = useState<RescanAggregateProgress | null>(null);
  const rescanAbortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(query);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    return () => {
      loadAbortRef.current?.abort();
      rescanAbortRef.current?.abort();
    };
  }, []);

  const loadKeywords = useCallback(async () => {
    const seq = ++loadSeqRef.current;
    loadAbortRef.current?.abort();
    const controller = new AbortController();
    loadAbortRef.current = controller;

    setLoading(true);
    setErrorMsg(null);
    try {
      const qs = new URLSearchParams();
      if (debouncedQuery.trim()) qs.set("query", debouncedQuery.trim());
      if (actionFilter) qs.set("action", actionFilter);
      if (includeInactive) qs.set("include_inactive", "true");
      qs.set("page", "1");
      qs.set("page_size", "100");
      const res = await api<PaginatedResponse<GatewayBannedKeyword>>(
        `admin/banned-keywords?${qs.toString()}`,
        { signal: controller.signal }
      );
      if (controller.signal.aborted || loadSeqRef.current !== seq) return;
      const list = Array.isArray(res?.items) ? res.items : [];
      setItems(list);
      setTotal(res?.total ?? list.length);
    } catch (err) {
      if (controller.signal.aborted || loadSeqRef.current !== seq) return;
      setErrorMsg(getErrorMessage(err, "Không thể tải danh sách từ khoá cấm lúc này."));
    } finally {
      if (!controller.signal.aborted && loadSeqRef.current === seq) {
        setLoading(false);
      }
    }
  }, [debouncedQuery, actionFilter, includeInactive]);

  useEffect(() => {
    void loadKeywords();
  }, [loadKeywords]);

  const handlePreview = async () => {
    const clean = newTerm.trim();
    if (!clean) {
      setFormError("Vui lòng nhập cụm từ cần kiểm tra trước khi xem trước ảnh hưởng.");
      return;
    }
    setFormError(null);
    setPreviewLoading(true);
    try {
      const res = await api<GatewayPreviewBannedKeywordResponse>("admin/banned-keywords/preview", {
        method: "POST",
        json: { term: clean },
      });
      setPreviewResult(res);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể xem trước sản phẩm bị ảnh hưởng."));
    } finally {
      setPreviewLoading(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const clean = newTerm.trim();
    if (!clean) {
      setFormError("Vui lòng nhập cụm từ cấm.");
      return;
    }
    if (clean.length > 120) {
      setFormError("Cụm từ cấm tối đa 120 ký tự.");
      return;
    }
    setFormError(null);
    setSubmitting(true);
    try {
      await api<{ keyword: GatewayBannedKeyword }>("admin/banned-keywords", {
        method: "POST",
        json: {
          term: clean,
          category: newCategory,
          action: newAction,
        },
      });
      setNewTerm("");
      setPreviewResult(null);
      await loadKeywords();
      onMutated?.();
      showToast(`Đã thêm từ khoá cấm "${clean}".`);
    } catch (err) {
      setFormError(getErrorMessage(err, "Không thể thêm từ khoá cấm."));
    } finally {
      setSubmitting(false);
    }
  };

  const startEdit = (kw: GatewayBannedKeyword) => {
    setEditingId(kw.id);
    setEditTerm(kw.term);
    setEditCategory(kw.category || "OTHER");
    setEditAction(kw.action.toUpperCase() === "REVIEW" ? "REVIEW" : "BLOCK");
    setEditActive(kw.is_active);
    setEditError(null);
  };

  const handleSaveEdit = async (kwId: string) => {
    const clean = editTerm.trim();
    if (!clean) {
      setEditError("Từ khoá cấm không được để trống.");
      return;
    }
    setEditError(null);
    setBusyId(kwId);
    try {
      await api<{ keyword: GatewayBannedKeyword }>(`admin/banned-keywords/${kwId}`, {
        method: "PUT",
        json: {
          term: clean,
          category: editCategory,
          action: editAction,
          is_active: editActive,
        },
      });
      setEditingId(null);
      await loadKeywords();
      onMutated?.();
      showToast(`Đã cập nhật từ khoá cấm "${clean}".`);
    } catch (err) {
      setEditError(getErrorMessage(err, "Không thể cập nhật từ khoá cấm."));
    } finally {
      setBusyId(null);
    }
  };

  const handleToggleActive = async (kw: GatewayBannedKeyword) => {
    setBusyId(kw.id);
    try {
      await api<{ keyword: GatewayBannedKeyword }>(`admin/banned-keywords/${kw.id}`, {
        method: "PUT",
        json: {
          term: kw.term,
          category: kw.category,
          action: kw.action,
          is_active: !kw.is_active,
        },
      });
      await loadKeywords();
      onMutated?.();
      showToast(
        !kw.is_active
          ? `Đã bật quy tắc từ khoá "${kw.term}".`
          : `Đã tạm tắt quy tắc từ khoá "${kw.term}".`
      );
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể thay đổi trạng thái từ khoá cấm."));
    } finally {
      setBusyId(null);
    }
  };

  const handleDelete = async (kw: GatewayBannedKeyword) => {
    setBusyId(kw.id);
    try {
      await api<{ ok: boolean }>(`admin/banned-keywords/${kw.id}`, {
        method: "DELETE",
      });
      setConfirmDeleteId(null);
      await loadKeywords();
      onMutated?.();
      showToast(`Đã xóa từ khoá cấm "${kw.term}".`);
    } catch (err) {
      showToast(getErrorMessage(err, "Không thể xóa từ khoá cấm."));
    } finally {
      setBusyId(null);
    }
  };

  const handleStopRescan = () => {
    rescanAbortRef.current?.abort();
  };

  const handleRescan = async () => {
    if (rescanning) return;
    rescanAbortRef.current?.abort();
    const controller = new AbortController();
    rescanAbortRef.current = controller;

    setRescanning(true);
    setRescanStatus("running");
    setRescanFailedBatch(null);
    setRescanError(null);
    setRescanProgress(null);
    try {
      const result = await runBannedKeywordRescanBatches(
        (req, signal) =>
          api<GatewayRescanProductsResponse>("admin/banned-keywords/rescan", {
            method: "POST",
            json: req,
            signal,
          }),
        { dryRun, signal: controller.signal },
        (progress) => setRescanProgress(progress)
      );
      setRescanProgress(result);
      if (result.aborted) {
        setRescanStatus("stopped");
        showToast(`Đã dừng quét sau ${formatInt(result.batches)} lô.`);
      } else {
        setRescanStatus("completed");
        showToast(
          dryRun
            ? `Đã chạy thử quét ${formatInt(result.scannedCount)} sản phẩm (${formatInt(result.blockedCount)} chặn, ${formatInt(result.flaggedCount)} gắn cờ).`
            : `Hoàn tất quét ${formatInt(result.scannedCount)} sản phẩm (${formatInt(result.blockedCount)} từ chối/gỡ, ${formatInt(result.flaggedCount)} đưa về chờ duyệt).`
        );
      }
    } catch (err) {
      if (controller.signal.aborted) {
        setRescanStatus("stopped");
        return;
      }
      if (err instanceof RescanBatchInterruptedError) {
        setRescanProgress(err.partialProgress);
        setRescanFailedBatch(err.failedBatch);
        setRescanStatus("interrupted");
        setRescanError(
          getErrorMessage(err.cause, `Quá trình quét bị gián đoạn ở lô ${err.failedBatch}.`)
        );
      } else {
        setRescanStatus("interrupted");
        setRescanFailedBatch((prev) => prev ?? 1);
        setRescanError(getErrorMessage(err, "Quá trình quét lại sản phẩm gặp lỗi."));
      }
    } finally {
      if (rescanAbortRef.current === controller) {
        rescanAbortRef.current = null;
      }
      setRescanning(false);
    }
  };

  return (
    <div className={shared.sectionStack}>
      <section className={shared.panel} aria-labelledby="kw-create-heading">
        <div className={shared.panelHeader}>
          <h2 id="kw-create-heading" className={shared.panelTitle}>
            Thêm quy tắc từ khoá cấm & xem trước ảnh hưởng
          </h2>
          <span className={shared.panelMeta}>
            Từ có dấu so đúng dấu, chữ không dấu khớp cả bản có dấu; cụm từ từ 2 tiếng trở lên khớp
            cả khi viết không dấu hoặc tách chữ.
          </span>
        </div>

        <form onSubmit={(e) => void handleCreate(e)} noValidate className={shared.detailGrid}>
          <div className={shared.detailField}>
            <label htmlFor="kw-new-term" className={shared.fieldLabel}>
              Cụm từ cấm
            </label>
            <input
              id="kw-new-term"
              type="text"
              className={shared.textInput}
              value={newTerm}
              onChange={(e) => {
                setNewTerm(e.target.value);
                if (formError) setFormError(null);
              }}
              placeholder="Ví dụ: hàng giả, zalo riêng, chuyển khoản ngoài..."
              maxLength={120}
            />
          </div>

          <div className={shared.detailField}>
            <label htmlFor="kw-new-category" className={shared.fieldLabel}>
              Nhóm vi phạm
            </label>
            <select
              id="kw-new-category"
              className={shared.toolbarSelect}
              style={{ height: 38 }}
              value={newCategory}
              onChange={(e) => setNewCategory(e.target.value)}
            >
              {KEYWORD_CATEGORIES.map((cat) => (
                <option key={cat.value} value={cat.value}>
                  {cat.label}
                </option>
              ))}
            </select>
          </div>

          <div className={shared.detailField}>
            <label htmlFor="kw-new-action" className={shared.fieldLabel}>
              Hành động áp dụng
            </label>
            <select
              id="kw-new-action"
              className={shared.toolbarSelect}
              style={{ height: 38 }}
              value={newAction}
              onChange={(e) => setNewAction(e.target.value as GatewayBannedKeywordAction)}
            >
              <option value="BLOCK">Chặn đăng bán & đánh giá (BLOCK)</option>
              <option value="REVIEW">Gắn cờ chuyển duyệt thủ công (REVIEW)</option>
            </select>
          </div>

          {formError && (
            <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
              <p className={shared.validationError} role="alert">
                {formError}
              </p>
            </div>
          )}

          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
            <div className={shared.detailButtons}>
              <Button
                type="button"
                variant="outline"
                size="sm"
                shape="app"
                disabled={previewLoading || submitting}
                onClick={() => void handlePreview()}
              >
                {previewLoading ? "Đang kiểm tra..." : "Xem trước ảnh hưởng"}
              </Button>
              <Button
                type="submit"
                variant="primary"
                size="sm"
                shape="app"
                disabled={submitting || previewLoading}
              >
                {submitting ? "Đang lưu..." : "Lưu từ khoá cấm"}
              </Button>
            </div>
          </div>
        </form>

        {previewResult && (
          <div className={shared.detailWell} style={{ marginTop: 16 }}>
            <div className={shared.detailHeader}>
              <h3 className={shared.detailHeading}>
                Kết quả xem trước: {formatInt(previewResult.affected_count)} sản phẩm bị ảnh hưởng
              </h3>
              <span className={shared.panelMeta}>
                Hiển thị tối đa {formatInt(previewResult.samples?.length ?? 0)} mẫu
              </span>
            </div>
            {(previewResult.samples?.length ?? 0) === 0 ? (
              <p className={shared.detailValPlain}>
                Không có sản phẩm đang bán hoặc chờ duyệt nào khớp với cụm từ này.
              </p>
            ) : (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Sản phẩm</th>
                        <th>Trạng thái</th>
                        <th>Từ khớp</th>
                        <th>Mức xử lý</th>
                      </tr>
                    </thead>
                    <tbody>
                      {previewResult.samples.map((s) => {
                        const statusMeta = getAdminProductStatusMeta(s.status);
                        const actMeta = formatActionPill(s.highest_action);
                        return (
                          <tr key={s.product_id}>
                            <td>
                              <div className={shared.cellPrimary}>{s.name}</div>
                              <div className={shared.cellSub}>/{s.slug}</div>
                            </td>
                            <td>
                              <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                            </td>
                            <td>{(s.matched_terms ?? []).join(", ") || "—"}</td>
                            <td>
                              <StatusPill label={actMeta.label} tone={actMeta.tone} />
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
                <div className={shared.mobileList}>
                  {previewResult.samples.map((s) => {
                    const statusMeta = getAdminProductStatusMeta(s.status);
                    const actMeta = formatActionPill(s.highest_action);
                    return (
                      <article key={s.product_id} className={shared.mobileItem}>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{s.name}</span>
                          <StatusPill label={actMeta.label} tone={actMeta.tone} />
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            Trạng thái: {statusMeta.label} · Khớp:{" "}
                            {(s.matched_terms ?? []).join(", ")}
                          </span>
                        </div>
                      </article>
                    );
                  })}
                </div>
              </>
            )}
          </div>
        )}
      </section>

      <section className={shared.panel} aria-labelledby="kw-rescan-heading">
        <div className={shared.panelHeader}>
          <h2 id="kw-rescan-heading" className={shared.panelTitle}>
            Quét lại sản phẩm đang bán theo danh sách từ khoá cấm
          </h2>
          <span className={shared.panelMeta}>Xử lý theo lô 500 sản phẩm</span>
        </div>

        <p className={shared.pageSubtitle} style={{ marginTop: 0, marginBottom: 12 }}>
          Quét toàn bộ sản phẩm đang hoạt động theo các quy tắc đang bật và danh mục hàng cấm. Quy
          tắc BLOCK chuyển sản phẩm sang từ chối; quy tắc REVIEW chuyển về hàng đợi chờ duyệt.
        </p>

        <div className={shared.detailButtons}>
          <label
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 8,
              fontSize: 14,
              cursor: "pointer",
            }}
          >
            <input
              type="checkbox"
              checked={dryRun}
              onChange={(e) => setDryRun(e.target.checked)}
              disabled={rescanning}
            />
            <span>Chạy thử (chỉ thống kê, không đổi trạng thái sản phẩm)</span>
          </label>

          <Button
            type="button"
            variant={dryRun ? "outline" : "danger"}
            size="sm"
            shape="app"
            disabled={rescanning}
            onClick={() => void handleRescan()}
          >
            {rescanning
              ? `Đang quét lô ${rescanProgress ? rescanProgress.batches + 1 : 1}...`
              : dryRun
                ? "Chạy thử quét lại SP đang bán"
                : "Quét lại SP đang bán"}
          </Button>
          {rescanning && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              shape="app"
              onClick={handleStopRescan}
            >
              Dừng
            </Button>
          )}
        </div>

        {rescanError && (
          <p className={shared.validationError} role="alert" style={{ marginTop: 10 }}>
            {rescanError}
          </p>
        )}

        {rescanProgress && (
          <div className={shared.detailWell} style={{ marginTop: 14 }}>
            <div className={shared.detailGrid}>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Tiến độ quét</span>
                <span className={shared.detailVal}>
                  {rescanning
                    ? `Đang quét (${formatInt(rescanProgress.batches)} lô)`
                    : rescanStatus === "interrupted"
                      ? `Gián đoạn ở lô ${formatInt(rescanFailedBatch ?? rescanProgress.batches + 1)}`
                      : rescanStatus === "stopped"
                        ? `Đã dừng sau ${formatInt(rescanProgress.batches)} lô`
                        : `Hoàn tất (${formatInt(rescanProgress.batches)} lô)`}
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Đã quét</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(rescanProgress.scannedCount)} sản phẩm
                </span>
              </div>
              <div className={shared.detailField}>
                <span className={shared.detailKey}>Chặn / Gắn cờ duyệt</span>
                <span className={`${shared.detailVal} num`}>
                  {formatInt(rescanProgress.blockedCount)} chặn ·{" "}
                  {formatInt(rescanProgress.flaggedCount)} gắn cờ
                </span>
              </div>
            </div>

            {rescanProgress.samples.length > 0 && (
              <>
                <div className={shared.tableWrap}>
                  <table className={shared.dataTable}>
                    <thead>
                      <tr>
                        <th>Sản phẩm (tối đa 20 mẫu)</th>
                        <th>Trạng thái trước khi quét</th>
                        <th>Từ khoá khớp</th>
                        <th>Mức xử lý</th>
                      </tr>
                    </thead>
                    <tbody>
                      {rescanProgress.samples.map((s) => {
                        const statusMeta = getAdminProductStatusMeta(s.status);
                        const actMeta = formatActionPill(s.highest_action);
                        return (
                          <tr key={s.product_id}>
                            <td>
                              <div className={shared.cellPrimary}>{s.name}</div>
                              <div className={shared.cellSub}>/{s.slug}</div>
                            </td>
                            <td>
                              <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                            </td>
                            <td>{(s.matched_terms ?? []).join(", ") || "—"}</td>
                            <td>
                              <StatusPill label={actMeta.label} tone={actMeta.tone} />
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
                <div className={shared.mobileList}>
                  {rescanProgress.samples.map((s) => {
                    const statusMeta = getAdminProductStatusMeta(s.status);
                    const actMeta = formatActionPill(s.highest_action);
                    return (
                      <article key={s.product_id} className={shared.mobileItem}>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{s.name}</span>
                          <StatusPill label={actMeta.label} tone={actMeta.tone} />
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            Trước khi quét: {statusMeta.label} ·{" "}
                            {(s.matched_terms ?? []).join(", ")}
                          </span>
                        </div>
                      </article>
                    );
                  })}
                </div>
              </>
            )}
          </div>
        )}
      </section>

      <section className={shared.panel} aria-labelledby="kw-list-heading">
        <div className={shared.panelHeader}>
          <h2 id="kw-list-heading" className={shared.panelTitle}>
            Danh sách từ khoá cấm ({formatInt(total)})
          </h2>
          <Button variant="outline" size="sm" shape="app" onClick={() => void loadKeywords()}>
            Làm mới
          </Button>
        </div>

        <div className={shared.toolbar} style={{ marginTop: 8 }}>
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
                placeholder="Tìm từ khoá cấm..."
                aria-label="Tìm từ khoá cấm"
              />
            </div>

            <select
              className={shared.toolbarSelect}
              value={actionFilter}
              onChange={(e) => setActionFilter(e.target.value as "" | GatewayBannedKeywordAction)}
              aria-label="Lọc theo hành động"
            >
              <option value="">Tất cả hành động</option>
              <option value="BLOCK">Chặn (BLOCK)</option>
              <option value="REVIEW">Gắn cờ duyệt (REVIEW)</option>
            </select>
          </div>

          <div className={shared.toolbarRight}>
            <label
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 6,
                fontSize: 13,
                cursor: "pointer",
              }}
            >
              <input
                type="checkbox"
                checked={includeInactive}
                onChange={(e) => setIncludeInactive(e.target.checked)}
              />
              <span>Hiện cả quy tắc đã tắt</span>
            </label>
          </div>
        </div>

        {editError && (
          <p className={shared.validationError} role="alert" style={{ marginBottom: 10 }}>
            {editError}
          </p>
        )}

        {loading ? (
          <div className={shared.stateWrap}>
            <StateView
              kind="loading"
              surface="app"
              title="Đang tải danh sách từ khoá cấm..."
              rows={4}
            />
          </div>
        ) : errorMsg ? (
          <div className={shared.stateWrap}>
            <StateView
              kind="error"
              surface="app"
              title="Không thể tải từ khoá cấm"
              message={errorMsg}
              actionLabel="Thử lại"
              onAction={() => void loadKeywords()}
            />
          </div>
        ) : items.length === 0 ? (
          <p className={shared.emptyRow}>Chưa có từ khoá cấm nào khớp bộ lọc hiện tại.</p>
        ) : (
          <>
            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Từ khoá</th>
                    <th>Nhóm vi phạm</th>
                    <th>Hành động</th>
                    <th>Hiệu lực</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((kw) => {
                    const isEditing = editingId === kw.id;
                    const isBusy = busyId === kw.id;
                    const actMeta = formatActionPill(kw.action);
                    return (
                      <tr key={kw.id}>
                        <td>
                          {isEditing ? (
                            <input
                              type="text"
                              className={shared.textInput}
                              style={{ height: 34, maxWidth: 220 }}
                              value={editTerm}
                              onChange={(e) => setEditTerm(e.target.value)}
                              aria-label="Sửa cụm từ cấm"
                            />
                          ) : (
                            <>
                              <div className={shared.cellPrimary}>{kw.term}</div>
                              <div className={shared.cellSub}>
                                Chuẩn hóa: {kw.normalized_term}
                                {kw.updated_at ? ` · ${formatDateTimeVi(kw.updated_at)}` : ""}
                              </div>
                            </>
                          )}
                        </td>
                        <td>
                          {isEditing ? (
                            <select
                              className={shared.toolbarSelect}
                              value={editCategory}
                              onChange={(e) => setEditCategory(e.target.value)}
                              aria-label="Sửa nhóm vi phạm"
                            >
                              {KEYWORD_CATEGORIES.map((c) => (
                                <option key={c.value} value={c.value}>
                                  {c.label}
                                </option>
                              ))}
                            </select>
                          ) : (
                            formatCategoryLabel(kw.category)
                          )}
                        </td>
                        <td>
                          {isEditing ? (
                            <select
                              className={shared.toolbarSelect}
                              value={editAction}
                              onChange={(e) =>
                                setEditAction(e.target.value as GatewayBannedKeywordAction)
                              }
                              aria-label="Sửa hành động"
                            >
                              <option value="BLOCK">Chặn (BLOCK)</option>
                              <option value="REVIEW">Gắn cờ duyệt (REVIEW)</option>
                            </select>
                          ) : (
                            <StatusPill label={actMeta.label} tone={actMeta.tone} />
                          )}
                        </td>
                        <td>
                          {isEditing ? (
                            <ToggleSwitch
                              id={`kw-edit-active-${kw.id}`}
                              checked={editActive}
                              onChange={(next) => setEditActive(next)}
                              label={`Trạng thái từ khoá ${kw.term}`}
                            />
                          ) : (
                            <ToggleSwitch
                              id={`kw-active-${kw.id}`}
                              checked={kw.is_active}
                              onChange={() => {
                                if (!isBusy) void handleToggleActive(kw);
                              }}
                              label={`Bật hoặc tắt từ khoá ${kw.term}`}
                            />
                          )}
                        </td>
                        <td className={shared.colAction}>
                          <div style={{ display: "inline-flex", gap: 6, flexWrap: "wrap" }}>
                            {isEditing ? (
                              <>
                                <Button
                                  variant="primary"
                                  size="sm"
                                  shape="app"
                                  disabled={isBusy}
                                  onClick={() => void handleSaveEdit(kw.id)}
                                >
                                  {isBusy ? "Lưu..." : "Lưu"}
                                </Button>
                                <Button
                                  variant="outline"
                                  size="sm"
                                  shape="app"
                                  onClick={() => {
                                    setEditingId(null);
                                    setEditError(null);
                                  }}
                                >
                                  Hủy
                                </Button>
                              </>
                            ) : (
                              <>
                                <button
                                  type="button"
                                  className={shared.rowActionBtn}
                                  disabled={isBusy}
                                  onClick={() => startEdit(kw)}
                                >
                                  Sửa
                                </button>
                                {confirmDeleteId === kw.id ? (
                                  <>
                                    <Button
                                      variant="danger"
                                      size="sm"
                                      shape="app"
                                      disabled={isBusy}
                                      onClick={() => void handleDelete(kw)}
                                    >
                                      Xác nhận xóa
                                    </Button>
                                    <button
                                      type="button"
                                      className={shared.rowActionBtn}
                                      onClick={() => setConfirmDeleteId(null)}
                                    >
                                      Giữ lại
                                    </button>
                                  </>
                                ) : (
                                  <button
                                    type="button"
                                    className={shared.rowActionBtn}
                                    disabled={isBusy}
                                    onClick={() => setConfirmDeleteId(kw.id)}
                                  >
                                    Xóa
                                  </button>
                                )}
                              </>
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {items.map((kw) => {
                const isEditing = editingId === kw.id;
                const isBusy = busyId === kw.id;
                const actMeta = formatActionPill(kw.action);
                return (
                  <article key={kw.id} className={shared.mobileItem}>
                    {isEditing ? (
                      <div className={shared.detailGrid}>
                        <div className={shared.detailField}>
                          <label htmlFor={`m-kw-term-${kw.id}`} className={shared.fieldLabel}>
                            Từ khoá cấm
                          </label>
                          <input
                            id={`m-kw-term-${kw.id}`}
                            type="text"
                            className={shared.textInput}
                            value={editTerm}
                            onChange={(e) => setEditTerm(e.target.value)}
                          />
                        </div>
                        <div className={shared.detailField}>
                          <label htmlFor={`m-kw-cat-${kw.id}`} className={shared.fieldLabel}>
                            Nhóm vi phạm
                          </label>
                          <select
                            id={`m-kw-cat-${kw.id}`}
                            className={shared.toolbarSelect}
                            style={{ height: 38 }}
                            value={editCategory}
                            onChange={(e) => setEditCategory(e.target.value)}
                          >
                            {KEYWORD_CATEGORIES.map((c) => (
                              <option key={c.value} value={c.value}>
                                {c.label}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className={shared.detailField}>
                          <label htmlFor={`m-kw-act-${kw.id}`} className={shared.fieldLabel}>
                            Hành động
                          </label>
                          <select
                            id={`m-kw-act-${kw.id}`}
                            className={shared.toolbarSelect}
                            style={{ height: 38 }}
                            value={editAction}
                            onChange={(e) =>
                              setEditAction(e.target.value as GatewayBannedKeywordAction)
                            }
                          >
                            <option value="BLOCK">Chặn (BLOCK)</option>
                            <option value="REVIEW">Gắn cờ duyệt (REVIEW)</option>
                          </select>
                        </div>
                        <div className={shared.detailField}>
                          <span className={shared.fieldLabel}>Hiệu lực quy tắc</span>
                          <div style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                            <ToggleSwitch
                              id={`m-kw-edit-active-${kw.id}`}
                              checked={editActive}
                              onChange={(next) => setEditActive(next)}
                              label={`Trạng thái từ khoá ${kw.term}`}
                            />
                            <span className={shared.mobileMetaText}>
                              {editActive ? "Đang bật" : "Đã tắt"}
                            </span>
                          </div>
                        </div>
                        {editError && (
                          <div className={`${shared.detailField} ${shared.detailFieldWide}`}>
                            <p className={shared.validationError} role="alert">
                              {editError}
                            </p>
                          </div>
                        )}
                        <div className={shared.detailButtons}>
                          <Button
                            variant="primary"
                            size="sm"
                            shape="app"
                            disabled={isBusy}
                            onClick={() => void handleSaveEdit(kw.id)}
                          >
                            {isBusy ? "Lưu..." : "Lưu"}
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            shape="app"
                            onClick={() => {
                              setEditingId(null);
                              setEditError(null);
                            }}
                          >
                            Hủy
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <>
                        <div className={shared.mobileLine1}>
                          <span className={shared.mobileIdentity}>{kw.term}</span>
                          <StatusPill label={actMeta.label} tone={actMeta.tone} />
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            {formatCategoryLabel(kw.category)} ·{" "}
                            {kw.is_active ? "Đang bật" : "Đã tắt"}
                          </span>
                          <ToggleSwitch
                            id={`kw-m-active-${kw.id}`}
                            checked={kw.is_active}
                            onChange={() => {
                              if (!isBusy) void handleToggleActive(kw);
                            }}
                            label={`Bật hoặc tắt từ khoá ${kw.term}`}
                          />
                        </div>
                        <div className={shared.mobileLine2}>
                          <span className={shared.mobileMetaText}>
                            Chuẩn hóa: {kw.normalized_term}
                          </span>
                          <div className={shared.detailButtons}>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={isBusy}
                              onClick={() => startEdit(kw)}
                            >
                              Sửa
                            </button>
                            {confirmDeleteId === kw.id ? (
                              <>
                                <Button
                                  variant="danger"
                                  size="sm"
                                  shape="app"
                                  disabled={isBusy}
                                  onClick={() => void handleDelete(kw)}
                                >
                                  Xác nhận xóa
                                </Button>
                                <button
                                  type="button"
                                  className={shared.rowActionBtn}
                                  onClick={() => setConfirmDeleteId(null)}
                                >
                                  Giữ lại
                                </button>
                              </>
                            ) : (
                              <button
                                type="button"
                                className={shared.rowActionBtn}
                                disabled={isBusy}
                                onClick={() => setConfirmDeleteId(kw.id)}
                              >
                                Xóa
                              </button>
                            )}
                          </div>
                        </div>
                      </>
                    )}
                  </article>
                );
              })}
            </div>
          </>
        )}
      </section>
    </div>
  );
}
