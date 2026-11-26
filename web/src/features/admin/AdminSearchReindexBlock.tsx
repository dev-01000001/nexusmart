"use client";

import React, { useEffect, useState } from "react";
import { Button } from "@/components";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type {
  GatewaySearchReindexResponse,
  GatewaySearchReindexStatusResponse,
} from "@/lib/gateway-types";
import { getSearchReindexStatusMeta } from "./adminApiTypes";
import { useAdminContext } from "./AdminShell";
import { AdminConfirmDialog } from "./AdminUi";
import { useAdminCapability } from "./AdminUserContext";
import { StatusPill } from "./StatusPill";
import shared from "./adminShared.module.css";
import styles from "@/app/(admin)/admin/cau-hinh/page.module.css";

const POLL_INTERVAL_MS = 2000;
const MAX_POLL_ERRORS = 3;

export function AdminSearchReindexBlock() {
  const { showToast } = useAdminContext();
  const canReindex = useAdminCapability("search.reindex");

  const [confirmOpen, setConfirmOpen] = useState(false);
  const [starting, setStarting] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const [pollEpoch, setPollEpoch] = useState(0);
  const [job, setJob] = useState<GatewaySearchReindexStatusResponse | null>(null);

  const jobId = job?.job_id ?? "";
  const statusMeta = job ? getSearchReindexStatusMeta(job.status) : null;
  const isRunning = statusMeta?.state === "RUNNING";

  useEffect(() => {
    if (!canReindex || !jobId || !isRunning) return;

    let cancelled = false;
    let timerId = 0;
    let consecutiveErrors = 0;
    let activeController: AbortController | null = null;

    const poll = async () => {
      activeController = new AbortController();
      try {
        const next = await api<GatewaySearchReindexStatusResponse>(
          `/api/admin/search/reindex/${encodeURIComponent(jobId)}`,
          { signal: activeController.signal }
        );
        if (cancelled) return;
        consecutiveErrors = 0;
        setJob(next);
        setErrorMsg(null);
        const nextMeta = getSearchReindexStatusMeta(next.status);
        if (nextMeta.state === "RUNNING") {
          timerId = window.setTimeout(() => void poll(), POLL_INTERVAL_MS);
        } else if (nextMeta.state === "COMPLETED") {
          showToast(`Đã dựng lại chỉ mục (${formatInt(next.products_indexed)} sản phẩm).`);
        } else if (nextMeta.state === "FAILED") {
          showToast("Dựng lại chỉ mục thất bại.");
        }
      } catch (err) {
        if (cancelled) return;
        consecutiveErrors += 1;
        if (consecutiveErrors >= MAX_POLL_ERRORS) {
          setErrorMsg(getErrorMessage(err, "Không thể cập nhật trạng thái dựng lại chỉ mục."));
          return;
        }
        timerId = window.setTimeout(() => void poll(), POLL_INTERVAL_MS);
      }
    };

    timerId = window.setTimeout(() => void poll(), POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      window.clearTimeout(timerId);
      activeController?.abort();
    };
  }, [canReindex, jobId, isRunning, pollEpoch, showToast]);

  if (!canReindex) {
    return null;
  }

  const handleConfirmReindex = async () => {
    if (starting || isRunning) return;
    setStarting(true);
    setErrorMsg(null);
    try {
      const started = await api<GatewaySearchReindexResponse>("/api/admin/search/reindex", {
        method: "POST",
        body: {},
      });
      setJob({
        job_id: started.job_id,
        status: started.status || "RUNNING",
        products_indexed: 0,
        embeddings_updated: 0,
        error_message: "",
      });
      setConfirmOpen(false);
      showToast("Đã bắt đầu dựng lại chỉ mục tìm kiếm.");
    } catch (err) {
      setErrorMsg(getErrorMessage(err, "Không thể bắt đầu dựng lại chỉ mục lúc này."));
      setConfirmOpen(false);
    } finally {
      setStarting(false);
    }
  };

  return (
    <>
      <section className={styles.configRow} aria-labelledby="search-reindex-heading">
        <div className={styles.configLabelCol}>
          <h2 id="search-reindex-heading" className={styles.configBlockTitle}>
            Chỉ mục tìm kiếm
          </h2>
          <p className={styles.configBlockMeta}>
            Đồng bộ lại dữ liệu sản phẩm, danh mục và gian hàng cho tìm kiếm.
          </p>
        </div>

        <div className={styles.configPanel}>
          <div className={styles.reindexRow}>
            <Button
              type="button"
              variant="outline"
              size="sm"
              shape="app"
              loading={starting}
              disabled={isRunning}
              onClick={() => setConfirmOpen(true)}
            >
              Dựng lại chỉ mục
            </Button>

            {job && statusMeta && (
              <div className={styles.reindexStatus} role="status" aria-live="polite">
                <StatusPill label={statusMeta.label} tone={statusMeta.tone} />
                <span className={styles.configBlockMeta} title={job.job_id}>
                  #{job.job_id.slice(0, 8)}
                </span>
              </div>
            )}
          </div>

          {job && statusMeta && (
            <p className={styles.configBlockMeta}>
              {statusMeta.state === "RUNNING" &&
                (job.products_indexed > 0
                  ? `Đang đồng bộ ${formatInt(job.products_indexed)} sản phẩm…`
                  : "Đang chạy nền…")}
              {statusMeta.state === "COMPLETED" &&
                `Đã đồng bộ ${formatInt(job.products_indexed)} sản phẩm${
                  job.finished_at ? ` · ${formatDateTimeVi(job.finished_at)}` : ""
                }`}
              {statusMeta.state === "FAILED" &&
                (job.error_message || "Dựng lại chỉ mục thất bại. Vui lòng thử lại sau.")}
            </p>
          )}

          {errorMsg && (
            <div className={styles.reindexRow}>
              <p className={shared.validationError} role="alert">
                {errorMsg}
              </p>
              {isRunning && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  shape="app"
                  onClick={() => {
                    setErrorMsg(null);
                    setPollEpoch((n) => n + 1);
                  }}
                >
                  Kiểm tra lại
                </Button>
              )}
            </div>
          )}
        </div>
      </section>

      <AdminConfirmDialog
        open={confirmOpen}
        title="Dựng lại chỉ mục tìm kiếm?"
        description="Tiến trình chạy nền để đồng bộ lại dữ liệu tìm kiếm. Không thể khởi chạy thêm khi đang xử lý."
        confirmLabel="Dựng lại chỉ mục"
        busy={starting}
        onCancel={() => {
          if (!starting) setConfirmOpen(false);
        }}
        onConfirm={() => void handleConfirmReindex()}
      />
    </>
  );
}
