"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, CheckCircle2, RefreshCw, RotateCcw, Wand2 } from "lucide-react";
import { StatusBadge } from "@/components/app";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cloneTemplateConfigForShop, findPlatformTemplate } from "@/features/site/templateCatalog";
import { cn } from "@/lib/utils";
import type {
  PlatformTemplateDTO,
  SiteTemplateConfig,
  TemplatePageKey,
  TemplateValidationIssue,
} from "./siteEditorTypes";
import {
  MAX_CONFIG_BYTES,
  buildCleanTemplateConfigPayload,
  countAllSections,
  formatBytesVi,
  validateSiteTemplateJson,
} from "./siteEditorUtils";
import type { PreviewStorefrontDataset } from "./previewData";
import { PreviewToolbar } from "./PreviewToolbar";
import { SitePreviewContent } from "./SitePreviewContent";
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";

export interface TemplateCodeEditorTabProps {
  config: SiteTemplateConfig;
  templates: PlatformTemplateDTO[];
  previewDataset: PreviewStorefrontDataset;
  platformOrigin: string;
  onValidConfigChange: (next: SiteTemplateConfig) => void;
  onNotify?: (msg: string) => void;
}

const CODE_LINE_HEIGHT_PX = 20;

function serializePretty(cfg: SiteTemplateConfig): string {
  return JSON.stringify(buildCleanTemplateConfigPayload(cfg), null, 2);
}

function computeOffsetForLineCol(
  text: string,
  targetLine: number,
  targetCol: number
): {
  start: number;
  end: number;
} {
  const lines = text.split("\n");
  const clampedLine = Math.max(1, Math.min(targetLine, lines.length));
  let lineStart = 0;
  for (let i = 0; i < clampedLine - 1; i++) {
    lineStart += lines[i].length + 1;
  }
  const lineText = lines[clampedLine - 1] ?? "";
  const colOffset = Math.max(0, Math.min(targetCol - 1, lineText.length));
  const start = lineStart + colOffset;
  const end = lineStart + lineText.length;
  return { start, end: Math.max(start, end) };
}

export function TemplateCodeEditorTab({
  config,
  templates,
  previewDataset,
  platformOrigin,
  onValidConfigChange,
  onNotify,
}: TemplateCodeEditorTabProps) {
  const [codeState, setCodeState] = useState(() => {
    const text = serializePretty(config);
    return {
      text,
      validation: validateSiteTemplateJson(text),
    };
  });
  const codeText = codeState.text;
  const validation = codeState.validation;
  const [lastValidConfig, setLastValidConfig] = useState<SiteTemplateConfig>(config);
  const [previewPage, setPreviewPage] = useState<TemplatePageKey>("home");
  const [previewDevice, setPreviewDevice] = useState<DevicePreviewWidth>("1440");
  const [highlightedLine, setHighlightedLine] = useState<number | null>(null);

  const lastEmittedSerializedRef = useRef<string>(codeText);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const gutterRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const incoming = serializePretty(config);
    if (incoming !== lastEmittedSerializedRef.current) {
      lastEmittedSerializedRef.current = incoming;
      setCodeState({ text: incoming, validation: validateSiteTemplateJson(incoming) });
      setLastValidConfig(config);
      setHighlightedLine(null);
    }
  }, [config]);

  const byteCount = useMemo(() => new TextEncoder().encode(codeText).length, [codeText]);
  const lineCount = useMemo(() => Math.max(1, codeText.split("\n").length), [codeText]);

  const errorLines = useMemo(() => {
    const set = new Set<number>();
    for (const issue of validation.issues) {
      set.add(issue.line);
    }
    return set;
  }, [validation.issues]);

  const handleCodeChange = (nextText: string) => {
    const res = validateSiteTemplateJson(nextText);
    setCodeState({ text: nextText, validation: res });
    if (res.ok && res.config) {
      setLastValidConfig(res.config);
      setHighlightedLine(null);
      const serialized = serializePretty(res.config);
      lastEmittedSerializedRef.current = serialized;
      onValidConfigChange(res.config);
    }
  };

  const handleBeautify = () => {
    try {
      const parsed = JSON.parse(codeText);
      const formatted = JSON.stringify(parsed, null, 2);
      const res = validateSiteTemplateJson(formatted);
      setCodeState({ text: formatted, validation: res });
      if (res.ok && res.config) {
        setLastValidConfig(res.config);
        lastEmittedSerializedRef.current = serializePretty(res.config);
        onValidConfigChange(res.config);
      }
      onNotify?.("Đã định dạng lại mã JSON.");
    } catch {
      onNotify?.("Không thể định dạng vì mã JSON đang có lỗi cú pháp.");
    }
  };

  const handleSyncFromVisual = () => {
    const synced = serializePretty(config);
    lastEmittedSerializedRef.current = synced;
    setCodeState({ text: synced, validation: validateSiteTemplateJson(synced) });
    setLastValidConfig(config);
    setHighlightedLine(null);
    onNotify?.("Đã đồng bộ từ trình chỉnh sửa trực quan.");
  };

  const handleResetToBaseTemplate = () => {
    const baseTpl =
      templates.find((t) => t.id === config.template_id) ??
      findPlatformTemplate(config.template_id) ??
      templates[0];
    if (!baseTpl) return;
    const cloned = cloneTemplateConfigForShop(baseTpl, {
      brandName: config.theme.brand_name || previewDataset.shop.name,
      tagline: config.theme.tagline || previewDataset.shop.description,
      logoUrl: config.theme.logo_url || "",
    });
    const nextText = serializePretty(cloned);
    lastEmittedSerializedRef.current = nextText;
    setCodeState({ text: nextText, validation: validateSiteTemplateJson(nextText) });
    setLastValidConfig(cloned);
    setHighlightedLine(null);
    onValidConfigChange(cloned);
    onNotify?.(`Đã đặt lại theo mẫu gốc "${baseTpl.name}".`);
  };

  const handleJumpToIssue = (issue: TemplateValidationIssue) => {
    const ta = textareaRef.current;
    if (!ta) return;
    const { start, end } = computeOffsetForLineCol(codeText, issue.line, issue.column);
    ta.focus();
    ta.setSelectionRange(start, end);
    const targetScroll = Math.max(0, (issue.line - 4) * CODE_LINE_HEIGHT_PX);
    ta.scrollTop = targetScroll;
    if (gutterRef.current) {
      gutterRef.current.scrollTop = targetScroll;
    }
    setHighlightedLine(issue.line);
  };

  const handleScrollSync = () => {
    if (textareaRef.current && gutterRef.current) {
      gutterRef.current.scrollTop = textareaRef.current.scrollTop;
    }
  };

  const overByteLimit = byteCount > MAX_CONFIG_BYTES;

  return (
    <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-[minmax(380px,42%)_minmax(0,1fr)]">
      {/* Left pane: Line-aware JSON editor + clickable schema diagnostics table */}
      <Card aria-label="Trình sửa mã template JSON v2">
        <CardHeader>
          <CardTitle>Mã JSON v2</CardTitle>
          <CardAction>
            <Badge
              variant={overByteLimit ? "critical" : "neutral"}
              aria-label="Dung lượng cấu hình"
              className="tabular-nums"
            >
              {formatBytesVi(byteCount)} / {formatBytesVi(MAX_CONFIG_BYTES)} B
            </Badge>
          </CardAction>
        </CardHeader>

        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-1.5">
              <Button type="button" variant="outline" size="sm" onClick={handleBeautify}>
                <Wand2 className="size-3.5" aria-hidden="true" />
                <span>Định dạng</span>
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={handleSyncFromVisual}>
                <RefreshCw className="size-3.5" aria-hidden="true" />
                <span>Đồng bộ</span>
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={handleResetToBaseTemplate}>
                <RotateCcw className="size-3.5" aria-hidden="true" />
                <span>Đặt lại mẫu gốc</span>
              </Button>
            </div>
            <div aria-live="polite">
              {validation.ok ? (
                <StatusBadge tone="success" icon={CheckCircle2}>
                  Hợp lệ, WCAG đạt
                </StatusBadge>
              ) : (
                <StatusBadge tone="critical" icon={AlertCircle}>
                  {validation.issues.length} lỗi cần sửa
                </StatusBadge>
              )}
            </div>
          </div>

          <div className="flex h-[500px] overflow-hidden rounded-lg border border-input bg-card">
            <div
              ref={gutterRef}
              aria-hidden="true"
              className="w-12 shrink-0 overflow-hidden border-r border-border bg-muted/60 py-3 text-right font-mono text-[13px] leading-[20px] text-muted-foreground tabular-nums select-none"
            >
              {Array.from({ length: lineCount }, (_, i) => {
                const ln = i + 1;
                const isErr = errorLines.has(ln);
                const isFocused = highlightedLine === ln;
                return (
                  <div
                    key={ln}
                    className={cn(
                      "h-[20px] pr-2 pl-1",
                      isFocused
                        ? "bg-brand font-semibold text-brand-foreground"
                        : isErr
                          ? "bg-badge-critical-bg font-semibold text-badge-critical-ink"
                          : ""
                    )}
                  >
                    {ln}
                  </div>
                );
              })}
            </div>
            <textarea
              ref={textareaRef}
              value={codeText}
              onChange={(e) => handleCodeChange(e.target.value)}
              onScroll={handleScrollSync}
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
              wrap="off"
              style={{ tabSize: 2 }}
              aria-label="Nội dung mã JSON template v2"
              className="h-full min-w-0 flex-1 resize-none overflow-auto border-0 bg-card px-3.5 py-3 font-mono text-[13px] leading-[20px] whitespace-pre text-foreground outline-none focus-visible:ring-0"
            />
          </div>

          <div className="flex flex-wrap items-center justify-between gap-2 text-[13px] text-muted-foreground tabular-nums">
            <span>
              {lineCount} dòng · {countAllSections(lastValidConfig)} khối ·{" "}
              <code className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">
                {lastValidConfig.template_id}
              </code>
            </span>
            {!validation.ok && (
              <span className="font-medium text-badge-warning-ink">
                Giữ bản xem trước hợp lệ gần nhất.
              </span>
            )}
          </div>

          {!validation.ok && validation.issues.length > 0 && (
            <div
              className="overflow-hidden rounded-lg border border-destructive/30 bg-badge-critical-bg/40"
              role="alert"
              aria-label="Danh sách lỗi mã template"
            >
              <div className="border-b border-destructive/20 px-3 py-2 text-xs font-semibold text-badge-critical-ink">
                Bảng lỗi — nhấn vào từng dòng để nhảy tới vị trí chính xác
              </div>
              <div className="max-h-56 overflow-y-auto bg-card/85">
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead className="h-8 px-2.5 text-xs">Vị trí</TableHead>
                      <TableHead className="h-8 px-2.5 text-xs">Chi tiết lỗi</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {validation.issues.map((issue, idx) => (
                      <TableRow
                        key={`${issue.line}-${issue.column}-${issue.code}-${idx}`}
                        className="cursor-pointer hover:bg-badge-critical-bg/45"
                        onClick={() => handleJumpToIssue(issue)}
                      >
                        <TableCell className="h-auto px-2.5 py-2 align-top">
                          <button
                            type="button"
                            onClick={(e) => {
                              e.stopPropagation();
                              handleJumpToIssue(issue);
                            }}
                            className="inline-flex min-h-7 items-center rounded-sm bg-badge-critical-bg px-2 py-0.5 font-mono text-xs font-semibold text-badge-critical-ink hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                          >
                            Dòng {issue.line}:{issue.column}
                          </button>
                        </TableCell>
                        <TableCell className="h-auto px-2.5 py-2 align-top text-xs whitespace-normal text-foreground">
                          <div className="flex flex-wrap items-baseline gap-1.5">
                            <span className="font-mono text-[11px] font-medium text-badge-critical-ink">
                              [{issue.code}]
                            </span>
                            <code className="font-mono text-[11px] text-muted-foreground">
                              {issue.path}
                            </code>
                          </div>
                          <p className="mt-0.5 leading-snug text-foreground">{issue.message}</p>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Right pane: Live Preview with 5-page switcher & 390 / 768 / 1440 device frame */}
      <Card
        aria-label="Xem trước trực tiếp mã template"
        className="gap-0 overflow-hidden py-0 xl:sticky xl:top-[72px]"
      >
        <PreviewToolbar
          activePage={previewPage}
          onPageChange={setPreviewPage}
          device={previewDevice}
          onDeviceChange={setPreviewDevice}
        />

        <div className="overflow-hidden bg-muted/60 p-3 sm:p-4">
          <SitePreviewFrame
            device={previewDevice}
            title={`Xem trước mã template (${previewDevice}px)`}
            resetScrollKey={`${lastValidConfig.template_id}:${previewPage}:${previewDevice}`}
            viewportHeight={600}
          >
            <SitePreviewContent
              config={lastValidConfig}
              page={previewPage}
              shop={previewDataset.shop}
              products={previewDataset.products}
              categories={previewDataset.categories}
              vouchers={previewDataset.vouchers}
              platformOrigin={platformOrigin}
            />
          </SitePreviewFrame>
        </div>
      </Card>
    </div>
  );
}
