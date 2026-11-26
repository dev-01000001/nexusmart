"use client";

import React, { useCallback, useState } from "react";
import Link from "next/link";
import { AlertTriangle, Clock, RefreshCw, Sparkles } from "lucide-react";
import { EmptyState, ErrorState, MetricStrip, PageHeader } from "@/components/app";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ApiError, api } from "@/lib/api";
import { formatDateTimeVi, formatInt, formatVnd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { renderAssistantMarkdown } from "@/features/assistant/assistantMarkdown";
import {
  normalizeSellerReport,
  type SellerReportPeriod,
  type SellerReportResult,
} from "@/features/assistant/assistantTypes";

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
      <div className="space-y-4">
        <PageHeader title="Báo cáo AI" />
        <EmptyState
          icon={Sparkles}
          title="Gian hàng chưa được kích hoạt"
          description="Hoàn tất kích hoạt gian hàng để sử dụng báo cáo phân tích AI."
          action={
            <Button size="sm" asChild>
              <Link href="/seller">Về tổng quan</Link>
            </Button>
          }
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

  return (
    <div className="space-y-4">
      <PageHeader
        title="Báo cáo AI"
        subtitle="Tổng hợp và nhận định trực tiếp trên dữ liệu đơn hàng, doanh thu và tồn kho gian hàng."
        primaryAction={
          <div
            role="group"
            aria-label="Chọn khoảng thời gian báo cáo"
            className="inline-flex items-center gap-1 rounded-lg border border-border bg-card p-1 shadow-card"
          >
            {PERIOD_OPTIONS.map((opt) => {
              const active = period === opt.value;
              return (
                <Button
                  key={opt.value}
                  type="button"
                  size="sm"
                  variant={active ? "secondary" : "ghost"}
                  aria-pressed={active}
                  onClick={() => setPeriod(opt.value)}
                  className={cn(
                    "min-h-10 min-w-[64px] px-2.5 text-[13px] md:h-8 md:min-h-0",
                    active && "font-semibold"
                  )}
                >
                  {opt.label}
                </Button>
              );
            })}
          </div>
        }
      />

      <Card aria-label="Truy vấn báo cáo AI">
        <CardContent className="space-y-3 pt-4">
          <form className="flex flex-col gap-2 sm:flex-row" onSubmit={handleSubmit}>
            <Input
              type="text"
              className="min-h-10 flex-1 bg-card"
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              placeholder="Ví dụ: Sản phẩm nào có tỷ lệ hoàn hủy cao nhất trong 30 ngày qua?"
              maxLength={500}
              aria-label="Nhập câu hỏi phân tích kinh doanh"
            />
            <Button type="submit" size="default" className="min-h-10 shrink-0" disabled={loading}>
              <Sparkles className="size-4" aria-hidden="true" />
              <span>{loading ? "Đang phân tích..." : "Phân tích"}</span>
            </Button>
          </form>

          <div className="flex flex-wrap gap-2" role="group" aria-label="Câu hỏi gợi ý nhanh">
            {QUICK_QUESTIONS.map((chip) => (
              <Button
                key={chip.label}
                type="button"
                variant="outline"
                size="sm"
                disabled={loading}
                onClick={() => handleQuickChip(chip)}
                className="h-auto min-h-10 py-1.5 text-left text-[13px] font-normal whitespace-normal md:min-h-9"
              >
                {chip.label}
              </Button>
            ))}
          </div>

          {errorState && errorState.kind === "invalid" && (
            <p className="text-sm text-destructive" role="alert">
              {errorState.message}
            </p>
          )}

          {errorState && errorState.kind === "rate_limited" && (
            <Alert variant="destructive">
              <AlertTriangle className="size-4" aria-hidden="true" />
              <AlertTitle>Đã đạt giới hạn lượt phân tích</AlertTitle>
              <AlertDescription>{errorState.message}</AlertDescription>
            </Alert>
          )}
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_300px] lg:items-start">
        <div className="min-w-0 space-y-4">
          {loading ? (
            <Card role="status" aria-label="Đang tổng hợp số liệu và phân tích...">
              <CardHeader>
                <CardTitle>Đang tổng hợp số liệu và phân tích...</CardTitle>
                <CardDescription>
                  Hệ thống đang truy xuất đơn hàng và tồn kho kỳ {getPeriodLabel(period)}.
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <Skeleton className="h-16 w-full" />
                  <Skeleton className="h-16 w-full" />
                  <Skeleton className="h-16 w-full" />
                </div>
                <Skeleton className="h-4 w-3/4" />
                <Skeleton className="h-4 w-full" />
                <Skeleton className="h-4 w-5/6" />
              </CardContent>
            </Card>
          ) : errorState && errorState.kind === "unavailable" ? (
            <ErrorState
              title="Dịch vụ báo cáo AI tạm thời gián đoạn"
              description={errorState.message}
              onRetry={
                lastSubmitted
                  ? () => void runReport(lastSubmitted.question, lastSubmitted.period)
                  : undefined
              }
            />
          ) : activeReport ? (
            <div className="space-y-4">
              {activeReport.kpis.length > 0 && (
                <section aria-label="Chỉ số tổng hợp từ dữ liệu nguồn">
                  <MetricStrip
                    items={activeReport.kpis.slice(0, 4).map((kpi) => ({
                      label: kpi.label,
                      value: kpi.value,
                      hint: kpi.caption ? (
                        <span
                          className={cn(
                            kpi.tone === "neg" && "font-medium text-destructive",
                            kpi.tone === "pos" && "font-medium text-badge-success-ink"
                          )}
                        >
                          {kpi.caption}
                        </span>
                      ) : undefined,
                    }))}
                  />
                </section>
              )}

              <Card aria-labelledby="ai-narrative-heading">
                <CardHeader>
                  <CardTitle id="ai-narrative-heading">Nhận định dữ liệu</CardTitle>
                  <CardDescription>
                    {activeReport.question
                      ? `${activeReport.question} (${getPeriodLabel(activeReport.period)})`
                      : getPeriodLabel(activeReport.period)}
                  </CardDescription>
                  {lastSubmitted && (
                    <CardAction>
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={loading}
                        onClick={() => void runReport(lastSubmitted.question, lastSubmitted.period)}
                      >
                        <RefreshCw className="size-3.5" aria-hidden="true" />
                        <span>Chạy lại</span>
                      </Button>
                    </CardAction>
                  )}
                </CardHeader>
                <CardContent>
                  <div className="max-w-prose text-sm leading-6 text-foreground">
                    {renderAssistantMarkdown(activeReport.answer, {
                      stripUuids: false,
                      LinkComponent: Link,
                      classNames: {
                        paragraph: "text-sm leading-6 text-foreground [&:not(:last-child)]:mb-3",
                        list: "my-2 list-disc space-y-1.5 pl-5 text-sm leading-6 text-foreground",
                        strong: "font-semibold text-foreground",
                        link: "font-medium text-brand underline underline-offset-4 hover:text-brand/80",
                      },
                    })}
                  </div>
                </CardContent>
              </Card>

              {activeReport.rows.length > 0 && (
                <Card aria-labelledby="ai-source-table-heading">
                  <CardHeader>
                    <CardTitle id="ai-source-table-heading">
                      Bảng đối chiếu ({formatInt(activeReport.rows.length)})
                    </CardTitle>
                  </CardHeader>
                  <CardContent className="px-0 pb-0">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Sản phẩm / Hạng mục</TableHead>
                          <TableHead className="text-right">SL bán</TableHead>
                          <TableHead className="text-right">Doanh thu</TableHead>
                          {activeReport.rows.some((r) => Boolean(r.extra)) && (
                            <TableHead>Ghi chú</TableHead>
                          )}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {activeReport.rows.map((row, idx) => (
                          <TableRow key={`${row.label}-${idx}`}>
                            <TableCell className="whitespace-normal">
                              <div className="font-medium text-foreground">{row.label}</div>
                              {row.subLabel && (
                                <div className="text-xs text-muted-foreground">{row.subLabel}</div>
                              )}
                            </TableCell>
                            <TableCell className="text-right tabular-nums">
                              {typeof row.unitsSold === "number" ? formatInt(row.unitsSold) : "-"}
                            </TableCell>
                            <TableCell className="text-right font-medium tabular-nums">
                              {typeof row.revenueVnd === "number" ? formatVnd(row.revenueVnd) : "-"}
                            </TableCell>
                            {activeReport.rows.some((r) => Boolean(r.extra)) && (
                              <TableCell className="whitespace-normal text-muted-foreground">
                                {row.extra ?? "-"}
                              </TableCell>
                            )}
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </CardContent>
                </Card>
              )}

              {activeReport.rows.length === 0 &&
                activeReport.tableColumns &&
                activeReport.genericRows &&
                activeReport.genericRows.length > 0 && (
                  <Card aria-labelledby="ai-generic-table-heading">
                    <CardHeader>
                      <CardTitle id="ai-generic-table-heading">Số liệu chi tiết</CardTitle>
                    </CardHeader>
                    <CardContent className="px-0 pb-0">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            {activeReport.tableColumns.map((col) => (
                              <TableHead key={col}>{col}</TableHead>
                            ))}
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {activeReport.genericRows.map((row, rIdx) => (
                            <TableRow key={rIdx}>
                              {activeReport.tableColumns!.map((col) => {
                                const val = row[col];
                                const isNum = typeof val === "number";
                                return (
                                  <TableCell
                                    key={col}
                                    className={cn(
                                      isNum ? "text-right tabular-nums" : "whitespace-normal"
                                    )}
                                  >
                                    {isNum ? formatInt(val) : String(val ?? "-")}
                                  </TableCell>
                                );
                              })}
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </CardContent>
                  </Card>
                )}
            </div>
          ) : (
            <EmptyState
              icon={Sparkles}
              title="Chọn câu hỏi gợi ý hoặc nhập câu hỏi riêng"
              description="Phân tích trực tiếp trên dữ liệu đơn hàng, doanh thu và tồn kho gian hàng."
            />
          )}
        </div>

        <aside aria-labelledby="ai-history-heading">
          <Card>
            <CardHeader>
              <CardTitle id="ai-history-heading" className="flex items-center gap-2">
                <Clock className="size-4 text-muted-foreground" aria-hidden="true" />
                <span>Lịch sử phiên</span>
              </CardTitle>
              <CardAction>
                <Badge variant="neutral" className="tabular-nums">
                  {formatInt(history.length)}
                </Badge>
              </CardAction>
            </CardHeader>
            <CardContent>
              {history.length === 0 ? (
                <p className="text-[13px] text-muted-foreground">Câu hỏi đã chạy sẽ lưu tại đây.</p>
              ) : (
                <ul className="space-y-1.5">
                  {history.map((entry) => {
                    const isSelected =
                      activeReport?.question === entry.question &&
                      activeReport?.period === entry.period;
                    return (
                      <li key={entry.id}>
                        <button
                          type="button"
                          onClick={() => {
                            setQuestion(entry.question);
                            setPeriod(entry.period);
                            setErrorState(null);
                            setActiveReport(entry.result);
                            setLastSubmitted({ question: entry.question, period: entry.period });
                          }}
                          className={cn(
                            "flex w-full flex-col gap-1 rounded-lg border px-3 py-2.5 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                            isSelected
                              ? "border-brand/40 bg-secondary text-foreground"
                              : "border-transparent bg-muted/50 text-foreground hover:border-border hover:bg-accent/60"
                          )}
                        >
                          <span className="line-clamp-2 text-[13px] font-medium">
                            {entry.question}
                          </span>
                          <span className="flex items-center justify-between gap-2 text-xs text-muted-foreground tabular-nums">
                            <span>{getPeriodLabel(entry.period)}</span>
                            <span>{formatDateTimeVi(entry.createdAt)}</span>
                          </span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </CardContent>
          </Card>
        </aside>
      </div>
    </div>
  );
}
