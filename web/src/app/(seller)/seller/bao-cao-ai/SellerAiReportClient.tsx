"use client";

import React, { useCallback, useState } from "react";
import Link from "next/link";
import { Button, IconClock, IconRefresh, IconSpark, StateView } from "@/components";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { renderAssistantMarkdown } from "@/features/assistant/assistantMarkdown";
import {
  normalizeSellerReport,
  type SellerReportPeriod,
  type SellerReportResult,
} from "@/features/assistant/assistantTypes";
import common from "@/features/seller/SellerCommon.module.css";
import styles from "./page.module.css";

interface ReportHistoryEntry {
  id: string;
  question: string;
  period: SellerReportPeriod;
  createdAt: string;
  result: SellerReportResult;
}

const PERIOD_OPTIONS: Array<{ value: SellerReportPeriod; label: string }> = [
  { value: "7d", label: "7 ngày" },
  { value: "30d", label: "30 ngày" },
  { value: "90d", label: "90 ngày" },
];

const QUICK_QUESTIONS: Array<{ label: string; suggestedPeriod?: SellerReportPeriod }> = [
  { label: "Sản phẩm bán chạy nhất 7 ngày qua", suggestedPeriod: "7d" },
  { label: "Tóm tắt doanh thu và tỷ lệ hủy đơn tháng này", suggestedPeriod: "30d" },
  { label: "Những SKU sắp hết hàng cần nhập gấp", suggestedPeriod: "30d" },
  { label: "So sánh doanh thu tuần này với tuần trước", suggestedPeriod: "7d" },
];

function getPeriodLabel(period: SellerReportPeriod): string {
  return PERIOD_OPTIONS.find((p) => p.value === period)?.label ?? "30 ngày";
}

export interface SellerAiReportClientProps {
  hasShop: boolean;
}

export function SellerAiReportClient({ hasShop }: SellerAiReportClientProps) {
  const [period, setPeriod] = useState<SellerReportPeriod>("30d");
  const [question, setQuestion] = useState("");
  const [loading, setLoading] = useState(false);
  const [errorState, setErrorState] = useState<{
    kind: "rate_limited" | "unavailable" | "invalid";
    message: string;
  } | null>(null);
  const [activeReport, setActiveReport] = useState<SellerReportResult | null>(null);
  const [history, setHistory] = useState<ReportHistoryEntry[]>([]);
  const [lastSubmitted, setLastSubmitted] = useState<{
    question: string;
    period: SellerReportPeriod;
  } | null>(null);

  const runReport = useCallback(async (rawQuestion: string, targetPeriod: SellerReportPeriod) => {
    const cleanQuestion = rawQuestion.trim();
    if (cleanQuestion.length < 3) {
      setErrorState({
        kind: "invalid",
        message: "Vui lòng nhập câu hỏi tối thiểu 3 ký tự.",
      });
      return;
    }

    setLoading(true);
    setErrorState(null);
    setLastSubmitted({ question: cleanQuestion, period: targetPeriod });

    try {
      const res = await api<unknown>("/api/seller/assistant/report", {
        method: "POST",
        body: {
          question: cleanQuestion,
          period: targetPeriod,
        },
      });
      const normalized = normalizeSellerReport(res, {
        period: targetPeriod,
        question: cleanQuestion,
      });
      setActiveReport(normalized);
      const nowIso = new Date().toISOString();
      setHistory((prev) => {
        const entry: ReportHistoryEntry = {
          id: `rep-${Date.now()}`,
          question: cleanQuestion,
          period: targetPeriod,
          createdAt: nowIso,
          result: normalized,
        };
        const deduped = prev.filter(
          (item) => !(item.question === cleanQuestion && item.period === targetPeriod)
        );
        return [entry, ...deduped].slice(0, 10);
      });
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        setErrorState({
          kind: "rate_limited",
          message:
            err.message ||
            "Bạn đã dùng hết lượt phân tích báo cáo AI trong ngày. Vui lòng thử lại sau.",
        });
      } else if (err instanceof ApiError && err.status === 400) {
        setErrorState({
          kind: "invalid",
          message: err.message,
        });
      } else {
        setErrorState({
          kind: "unavailable",
          message:
            err instanceof ApiError
              ? err.message
              : "Dịch vụ báo cáo AI tạm thời chưa sẵn sàng. Bạn vui lòng thử lại sau.",
        });
      }
    } finally {
      setLoading(false);
    }
  }, []);

  if (!hasShop) {
    return (
      <div className={common.page}>
        <StateView
          kind="empty"
          surface="app"
          title="Gian hàng chưa được kích hoạt"
          message="Hoàn tất kích hoạt gian hàng để sử dụng báo cáo phân tích AI."
          actionLabel="Về tổng quan"
          actionHref="/seller"
        />
      </div>
    );
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    void runReport(question, period);
  };

  const handleQuickChip = (chip: { label: string; suggestedPeriod?: SellerReportPeriod }) => {
    const nextPeriod = chip.suggestedPeriod ?? period;
    setPeriod(nextPeriod);
    setQuestion(chip.label);
    void runReport(chip.label, nextPeriod);
  };

  const kpiStripClass =
    activeReport && activeReport.kpis.length === 3 ? common.balanceStrip3 : common.balanceStrip;

  return (
    <div className={common.page} data-ui="seller-ai-report">
      <header className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Báo cáo AI</h1>
        </div>
        <div className={common.headerActions}>
          <div className={common.segmented} role="group" aria-label="Chọn khoảng thời gian báo cáo">
            {PERIOD_OPTIONS.map((opt) => (
              <button
                key={opt.value}
                type="button"
                className={`${common.segmentedBtn} ${
                  period === opt.value ? common.segmentedBtnActive : ""
                }`}
                aria-pressed={period === opt.value}
                onClick={() => setPeriod(opt.value)}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>
      </header>

      <section className={common.card} aria-label="Truy vấn báo cáo AI">
        <form className={styles.queryForm} onSubmit={handleSubmit}>
          <input
            type="text"
            className={common.textInput}
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder="Ví dụ: Sản phẩm nào có tỷ lệ hoàn hủy cao nhất trong 30 ngày qua?"
            maxLength={500}
            aria-label="Nhập câu hỏi phân tích kinh doanh"
          />
          <Button type="submit" variant="ink" size="md" shape="app" disabled={loading}>
            <IconSpark size={15} />
            <span>{loading ? "Đang phân tích..." : "Phân tích"}</span>
          </Button>
        </form>

        <div className={styles.chipRow} role="group" aria-label="Câu hỏi gợi ý nhanh">
          {QUICK_QUESTIONS.map((chip) => (
            <button
              key={chip.label}
              type="button"
              className={styles.quickChip}
              disabled={loading}
              onClick={() => handleQuickChip(chip)}
            >
              {chip.label}
            </button>
          ))}
        </div>

        {errorState && errorState.kind === "invalid" && (
          <p className={`${common.fieldError} ${styles.inlineError}`} role="alert">
            {errorState.message}
          </p>
        )}

        {errorState && errorState.kind === "rate_limited" && (
          <div className={styles.limitBanner} role="alert">
            <strong>Đã đạt giới hạn lượt phân tích</strong>
            <span>{errorState.message}</span>
          </div>
        )}
      </section>

      <div className={styles.workspaceGrid}>
        <div className={styles.mainCol}>
          {loading ? (
            <div className={common.card}>
              <StateView
                kind="loading"
                surface="app"
                skeletonVariant="table"
                title="Đang tổng hợp số liệu và phân tích..."
                rows={4}
              />
            </div>
          ) : errorState && errorState.kind === "unavailable" ? (
            <StateView
              kind="error"
              surface="app"
              title="Dịch vụ báo cáo AI tạm thời gián đoạn"
              message={errorState.message}
              actionLabel={lastSubmitted ? "Thử phân tích lại" : undefined}
              onAction={
                lastSubmitted
                  ? () => void runReport(lastSubmitted.question, lastSubmitted.period)
                  : undefined
              }
            />
          ) : activeReport ? (
            <div className={styles.reportStack}>
              {activeReport.kpis.length > 0 && (
                <div className={kpiStripClass} aria-label="Chỉ số tổng hợp từ dữ liệu nguồn">
                  {activeReport.kpis.slice(0, 4).map((kpi) => (
                    <div key={kpi.label} className={common.balanceCell}>
                      <span className={common.balanceLabel}>{kpi.label}</span>
                      <span className={common.balanceValue}>{kpi.value}</span>
                      {kpi.caption && (
                        <span
                          className={
                            kpi.tone === "neg"
                              ? common.deltaNeg
                              : kpi.tone === "pos"
                                ? common.deltaPos
                                : common.balanceCaption
                          }
                        >
                          {kpi.caption}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}

              <section className={common.card} aria-labelledby="ai-narrative-heading">
                <div className={common.cardHeader}>
                  <div>
                    <h2 id="ai-narrative-heading" className={common.cardTitle}>
                      Nhận định dữ liệu
                    </h2>
                    <span className={common.cardMeta}>
                      {activeReport.question
                        ? `${activeReport.question} (${getPeriodLabel(activeReport.period)})`
                        : getPeriodLabel(activeReport.period)}
                    </span>
                  </div>
                  {lastSubmitted && (
                    <Button
                      variant="outline"
                      size="sm"
                      shape="app"
                      disabled={loading}
                      onClick={() => void runReport(lastSubmitted.question, lastSubmitted.period)}
                    >
                      <IconRefresh size={14} />
                      <span>Chạy lại</span>
                    </Button>
                  )}
                </div>

                <div className={styles.narrativeBody}>
                  {renderAssistantMarkdown(activeReport.answer, {
                    stripUuids: false,
                    LinkComponent: Link,
                    classNames: {
                      paragraph: styles.narrativeParagraph,
                      list: styles.narrativeList,
                      strong: styles.narrativeStrong,
                      link: styles.narrativeLink,
                    },
                  })}
                </div>
              </section>

              {activeReport.rows.length > 0 && (
                <section className={common.card} aria-labelledby="ai-source-table-heading">
                  <div className={common.cardHeader}>
                    <h2 id="ai-source-table-heading" className={common.cardTitle}>
                      Bảng đối chiếu ({formatInt(activeReport.rows.length)})
                    </h2>
                  </div>
                  <div className={common.tableWrap}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Sản phẩm / Hạng mục</th>
                          <th className={common.numCol}>SL bán</th>
                          <th className={common.numCol}>Doanh thu</th>
                          {activeReport.rows.some((r) => Boolean(r.extra)) && <th>Ghi chú</th>}
                        </tr>
                      </thead>
                      <tbody>
                        {activeReport.rows.map((row, idx) => (
                          <tr key={`${row.label}-${idx}`}>
                            <td>
                              <span>{row.label}</span>
                              {row.subLabel && (
                                <span className={common.cellSub}>{row.subLabel}</span>
                              )}
                            </td>
                            <td className={common.numCol}>
                              {typeof row.unitsSold === "number" ? formatInt(row.unitsSold) : "-"}
                            </td>
                            <td className={common.numCol}>
                              {typeof row.revenueVnd === "number" ? formatVnd(row.revenueVnd) : "-"}
                            </td>
                            {activeReport.rows.some((r) => Boolean(r.extra)) && (
                              <td>{row.extra ?? "-"}</td>
                            )}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              )}

              {activeReport.rows.length === 0 &&
                activeReport.tableColumns &&
                activeReport.genericRows &&
                activeReport.genericRows.length > 0 && (
                  <section className={common.card} aria-labelledby="ai-generic-table-heading">
                    <div className={common.cardHeader}>
                      <h2 id="ai-generic-table-heading" className={common.cardTitle}>
                        Số liệu chi tiết
                      </h2>
                    </div>
                    <div className={common.tableWrap}>
                      <table className={common.dataTable}>
                        <thead>
                          <tr>
                            {activeReport.tableColumns.map((col) => (
                              <th key={col}>{col}</th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {activeReport.genericRows.map((row, rIdx) => (
                            <tr key={rIdx}>
                              {activeReport.tableColumns!.map((col) => {
                                const val = row[col];
                                const isNum = typeof val === "number";
                                return (
                                  <td key={col} className={isNum ? common.numCol : undefined}>
                                    {isNum ? formatInt(val) : String(val ?? "-")}
                                  </td>
                                );
                              })}
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </section>
                )}
            </div>
          ) : (
            <section className={common.card}>
              <StateView
                kind="empty"
                surface="app"
                title="Chọn câu hỏi gợi ý hoặc nhập câu hỏi riêng"
                message="Phân tích trực tiếp trên dữ liệu đơn hàng, doanh thu và tồn kho gian hàng."
              />
            </section>
          )}
        </div>

        <aside className={styles.historyCol} aria-labelledby="ai-history-heading">
          <section className={common.card}>
            <div className={common.cardHeader}>
              <div className={styles.historyTitleRow}>
                <IconClock size={15} />
                <h2 id="ai-history-heading" className={common.cardTitle}>
                  Lịch sử phiên
                </h2>
              </div>
              <span className={common.cardMeta}>{formatInt(history.length)}</span>
            </div>

            {history.length === 0 ? (
              <div className={common.inlineEmpty}>Câu hỏi đã chạy sẽ lưu tại đây.</div>
            ) : (
              <ul className={styles.historyList}>
                {history.map((entry) => {
                  const isSelected =
                    activeReport?.question === entry.question &&
                    activeReport?.period === entry.period;
                  return (
                    <li key={entry.id}>
                      <button
                        type="button"
                        className={`${styles.historyItem} ${
                          isSelected ? styles.historyItemActive : ""
                        }`}
                        onClick={() => {
                          setQuestion(entry.question);
                          setPeriod(entry.period);
                          setErrorState(null);
                          setActiveReport(entry.result);
                          setLastSubmitted({ question: entry.question, period: entry.period });
                        }}
                      >
                        <span className={styles.historyQuestion}>{entry.question}</span>
                        <span className={styles.historyMeta}>
                          <span>{getPeriodLabel(entry.period)}</span>
                          <span>{formatDateTimeVi(entry.createdAt)}</span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        </aside>
      </div>
    </div>
  );
}
