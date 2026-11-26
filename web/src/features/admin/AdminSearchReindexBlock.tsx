"use client";

import React, { useEffect, useState } from "react";
import { AlertCircle, RefreshCw } from "lucide-react";
import { StatusBadge } from "@/components/app";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { api, getErrorMessage } from "@/lib/api";
import { formatDateTimeVi, formatInt } from "@/lib/format";
import type {
  GatewaySearchReindexResponse,
  GatewaySearchReindexStatusResponse,
} from "@/lib/gateway-types";
import { getSearchReindexStatusMeta } from "./adminApiTypes";
import { useAdminContext } from "./AdminShell";
import { useAdminCapability } from "./AdminUserContext";
import { ConfirmActionDialog, searchReindexStateTone } from "./config/adminUiHelpers";

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
      <section
        className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] md:items-start"
        aria-labelledby="search-reindex-heading"
      >
        <div className="space-y-1">
          <h2 id="search-reindex-heading" className="text-sm font-semibold text-foreground">
            Chỉ mục tìm kiếm
          </h2>
          <p className="text-[13px] leading-[18px] text-muted-foreground">
            Đồng bộ lại dữ liệu sản phẩm, danh mục và gian hàng cho tìm kiếm.
          </p>
        </div>

        <Card>
          <CardContent className="space-y-3 pt-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={starting || isRunning}
                onClick={() => setConfirmOpen(true)}
              >
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>{starting ? "Đang khởi chạy..." : "Dựng lại chỉ mục"}</span>
              </Button>

              {job && statusMeta ? (
                <div
                  className="inline-flex flex-wrap items-center gap-2"
                  role="status"
                  aria-live="polite"
                >
                  <StatusBadge tone={searchReindexStateTone(statusMeta.state)}>
                    {statusMeta.label}
                  </StatusBadge>
                  <span className="text-xs text-muted-foreground tabular-nums" title={job.job_id}>
                    #{job.job_id.slice(0, 8)}
                  </span>
                </div>
              ) : null}
            </div>

            {job && statusMeta ? (
              <p className="text-[13px] text-muted-foreground tabular-nums">
                {statusMeta.state === "RUNNING"
                  ? job.products_indexed > 0
                    ? `Đang đồng bộ ${formatInt(job.products_indexed)} sản phẩm…`
                    : "Đang chạy nền…"
                  : null}
                {statusMeta.state === "COMPLETED"
                  ? `Đã đồng bộ ${formatInt(job.products_indexed)} sản phẩm${
                      job.finished_at ? ` · ${formatDateTimeVi(job.finished_at)}` : ""
                    }`
                  : null}
                {statusMeta.state === "FAILED"
                  ? job.error_message || "Dựng lại chỉ mục thất bại. Vui lòng thử lại sau."
                  : null}
              </p>
            ) : null}

            {errorMsg ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden="true" />
                <AlertDescription className="flex w-full flex-wrap items-center justify-between gap-2">
                  <span>{errorMsg}</span>
                  {isRunning ? (
                    <Button
                      type="button"
                      variant="outline"
                      size="xs"
                      onClick={() => {
                        setErrorMsg(null);
                        setPollEpoch((n) => n + 1);
                      }}
                    >
                      Kiểm tra lại
                    </Button>
                  ) : null}
                </AlertDescription>
              </Alert>
            ) : null}
          </CardContent>
        </Card>
      </section>

      <ConfirmActionDialog
        open={confirmOpen}
        title="Dựng lại chỉ mục tìm kiếm?"
        description="Tiến trình chạy nền để đồng bộ lại dữ liệu tìm kiếm. Không thể khởi chạy thêm khi đang xử lý."
        confirmLabel="Dựng lại chỉ mục"
        confirmVariant="default"
        busy={starting}
        onCancel={() => {
          if (!starting) setConfirmOpen(false);
        }}
        onConfirm={() => void handleConfirmReindex()}
      />
    </>
  );
}
