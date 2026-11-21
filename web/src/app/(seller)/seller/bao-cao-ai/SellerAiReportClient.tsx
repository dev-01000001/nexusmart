"use client";

import React, { useCallback, useState } from "react";
import { Button, IconClock, IconRefresh, IconSpark, StateView } from "@/components";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { useSellerContext } from "@/features/seller/SellerShell";
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

function renderNarrativeBlocks(text: string): React.ReactNode {
  const rawLines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);

  const elements: React.ReactNode[] = [];
  let bullets: string[] = [];

  const flushBullets = (keyPrefix: string) => {
    if (bullets.length === 0) return;
    elements.push(
      <ul key={`list-${keyPrefix}`} className={styles.narrativeList}>
        {bullets.map((b, idx) => (
          <li key={`${keyPrefix}-${idx}`}>{b}</li>
        ))}
      </ul>
    );
    bullets = [];
  };

  rawLines.forEach((line, idx) => {
    if (/^([-*•])\s+/.test(line)) {
      bullets.push(line.replace(/^([-*•])\s+/, "").trim());
    } else {
      flushBullets(String(idx));
      elements.push(
        <p key={`p-${idx}`} className={styles.narrativeParagraph}>
          {line}
        </p>
      );
    }
  });
  flushBullets("end");

  return elements;
}

export interface SellerAiReportClientProps {
  hasShop: boolean;
}

export function SellerAiReportClient({ hasShop }: SellerAiReportClientProps) {
  const { shop } = useSellerContext();

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
        message: "Vui lòng nhập câu hỏi tối thiểu 3 ký tự để phân tích số liệu gian hàng.",
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
          message="Hoàn tất đăng ký và kích hoạt gian hàng để sử dụng báo cáo phân tích AI."
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
      <div className={common.pageHeader}>
        <div className={common.headerText}>
          <h1 className={common.title}>Báo cáo phân tích AI</h1>
          <p className={common.finding}>
            {shop?.name ?? "Gian hàng của bạn"} · Đặt câu hỏi bằng tiếng Việt tự nhiên trên dữ liệu
            đơn hàng, doanh thu và tồn kho thực tế
          </p>
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
      </div>

      <section className={common.card} aria-labelledby="ai-report-query-heading">
        <div className={common.cardHeader}>
          <div>
            <h2 id="ai-report-query-heading" className={common.cardTitle}>
              Đặt câu hỏi kinh doanh
            </h2>
            <span className={common.cardMeta}>
              Phạm vi phân tích hiện tại: {getPeriodLabel(period)} gần nhất (chỉ đọc số liệu gian
              hàng của bạn)
            </span>
          </div>
        </div>

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
            <span>{loading ? "Đang phân tích..." : "Phân tích báo cáo"}</span>
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
                title="Đang tổng hợp số liệu và phân tích báo cáo..."
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
                      Nhận định từ dữ liệu gian hàng
                    </h2>
                    <span className={common.cardMeta}>
                      {activeReport.question ? `Câu hỏi: "${activeReport.question}" · ` : ""}
                      Kỳ {getPeriodLabel(activeReport.period)}
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
                  {renderNarrativeBlocks(activeReport.answer)}
                </div>
              </section>

              {activeReport.rows.length > 0 && (
                <section className={common.card} aria-labelledby="ai-source-table-heading">
                  <div className={common.cardHeader}>
                    <div>
                      <h2 id="ai-source-table-heading" className={common.cardTitle}>
                        Bảng số liệu đối chiếu
                      </h2>
                      <span className={common.cardMeta}>
                        {formatInt(activeReport.rows.length)} dòng dữ liệu nguồn dùng để tổng hợp
                        báo cáo
                      </span>
                    </div>
                  </div>
                  <div className={common.tableWrap}>
                    <table className={common.dataTable}>
                      <thead>
                        <tr>
                          <th>Sản phẩm / Hạng mục</th>
                          <th className={common.numCol}>Số lượng bán</th>
                          <th className={common.numCol}>Doanh thu (VND)</th>
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
                        Bảng số liệu chi tiết
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
                title="Chọn một câu hỏi gợi ý hoặc nhập câu hỏi riêng"
                message="Báo cáo AI tổng hợp trực tiếp từ doanh thu, trạng thái đơn hàng và tồn kho hiện tại của gian hàng."
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
                  Lịch sử phiên này
                </h2>
              </div>
              <span className={common.cardMeta}>{formatInt(history.length)} câu hỏi</span>
            </div>

            {history.length === 0 ? (
              <div className={common.inlineEmpty}>
                Các câu hỏi bạn vừa chạy sẽ xuất hiện tại đây để mở lại nhanh.
              </div>
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
