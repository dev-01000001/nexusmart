"use client";

import React, { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components";
import { cloneTemplateConfigForShop, findPlatformTemplate } from "@/features/site/templateCatalog";
import common from "@/features/seller/SellerCommon.module.css";
import type {
  PlatformTemplateDTO,
  SiteTemplateConfig,
  TemplatePageKey,
  TemplateValidationIssue,
} from "./siteEditorTypes";
import {
  MAX_CONFIG_BYTES,
  TEMPLATE_PAGE_TABS,
  buildCleanTemplateConfigPayload,
  countAllSections,
  formatBytesVi,
  validateSiteTemplateJson,
} from "./siteEditorUtils";
import type { PreviewStorefrontDataset } from "./previewData";
import { SitePreviewContent } from "./SitePreviewContent";
import styles from "./SellerSiteEditor.module.css";

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

function computeOffsetForLineCol(text: string, targetLine: number, targetCol: number): {
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
  const initialPretty = useMemo(() => serializePretty(config), [config]);
  const [codeText, setCodeText] = useState<string>(initialPretty);
  const [lastValidConfig, setLastValidConfig] = useState<SiteTemplateConfig>(config);
  const [previewPage, setPreviewPage] = useState<TemplatePageKey>("home");
  const [previewViewport, setPreviewViewport] = useState<"desktop" | "mobile">("desktop");
  const [highlightedLine, setHighlightedLine] = useState<number | null>(null);

  const lastEmittedSerializedRef = useRef<string>(initialPretty);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const gutterRef = useRef<HTMLDivElement | null>(null);

  // Sync external updates (e.g. applying a template, editing visual controls, or rolling back)
  // without clobbering user typing when the update came from this code editor itself.
  useEffect(() => {
    const incoming = serializePretty(config);
    if (incoming !== lastEmittedSerializedRef.current) {
      lastEmittedSerializedRef.current = incoming;
      setCodeText(incoming);
      setLastValidConfig(config);
      setHighlightedLine(null);
    }
  }, [config]);

  const byteCount = useMemo(() => new TextEncoder().encode(codeText).length, [codeText]);
  const lineCount = useMemo(() => Math.max(1, codeText.split("\n").length), [codeText]);

  const validation = useMemo(() => validateSiteTemplateJson(codeText), [codeText]);
  const errorLines = useMemo(() => {
    const set = new Set<number>();
    for (const issue of validation.issues) {
      set.add(issue.line);
    }
    return set;
  }, [validation.issues]);

  const handleCodeChange = (nextText: string) => {
    setCodeText(nextText);
    const res = validateSiteTemplateJson(nextText);
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
      setCodeText(formatted);
      const res = validateSiteTemplateJson(formatted);
      if (res.ok && res.config) {
        setLastValidConfig(res.config);
        lastEmittedSerializedRef.current = serializePretty(res.config);
        onValidConfigChange(res.config);
      }
      onNotify?.("Đã định dạng lại mã JSON gọn 2 khoảng trắng.");
    } catch {
      onNotify?.("Không thể định dạng vì mã JSON đang có lỗi cú pháp. Hãy sửa dòng báo lỗi trước.");
    }
  };

  const handleSyncFromVisual = () => {
    const synced = serializePretty(config);
    lastEmittedSerializedRef.current = synced;
    setCodeText(synced);
    setLastValidConfig(config);
    setHighlightedLine(null);
    onNotify?.("Đã đồng bộ cấu hình mới nhất từ trình chỉnh sửa trực quan.");
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
    setCodeText(nextText);
    setLastValidConfig(cloned);
    setHighlightedLine(null);
    onValidConfigChange(cloned);
    onNotify?.(`Đã đặt lại cấu hình theo mẫu gốc "${baseTpl.name}".`);
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
    <div className={styles.codeWorkspace}>
      {/* Left pane: Line-aware JSON editor + strict schema diagnostics */}
      <section className={common.card} aria-label="Trình sửa mã template JSON v2">
        <div className={styles.codeHeaderRow}>
          <div>
            <h2 className={common.cardTitle}>Mã template (Declarative Theme JSON v2)</h2>
            <p className={common.cardMeta}>
              Chỉnh trực tiếp JSON chuẩn schema v2 — đồng bộ hai chiều với trình trực quan và kiểm
              tra bảo mật XSS/SSRF/WCAG tức thì.
            </p>
          </div>
          <span
            className={`${common.pill} ${overByteLimit ? common.pillNeg : ""}`}
            aria-label="Dung lượng cấu hình"
          >
            {formatBytesVi(byteCount)} / {formatBytesVi(MAX_CONFIG_BYTES)} byte
          </span>
        </div>

        <div className={styles.codeToolbar}>
          <div className={styles.codeToolbarActions}>
            <Button variant="outline" size="sm" shape="app" onClick={handleBeautify}>
              Định dạng JSON
            </Button>
            <Button variant="outline" size="sm" shape="app" onClick={handleSyncFromVisual}>
              Đồng bộ từ trình trực quan
            </Button>
            <Button variant="outline" size="sm" shape="app" onClick={handleResetToBaseTemplate}>
              Đặt lại theo mẫu gốc
            </Button>
          </div>
          <div className={styles.codeStatusWrap} aria-live="polite">
            {validation.ok ? (
              <span className={`${common.pill} ${common.pillPos}`}>
                Schema v2 hợp lệ · An toàn XSS/SSRF · WCAG đạt chuẩn
              </span>
            ) : (
              <span className={`${common.pill} ${common.pillNeg}`}>
                {validation.issues.length} lỗi cần sửa
              </span>
            )}
          </div>
        </div>

        <div className={styles.codeEditorShell}>
          <div
            ref={gutterRef}
            className={styles.codeGutter}
            aria-hidden="true"
          >
            {Array.from({ length: lineCount }, (_, i) => {
              const ln = i + 1;
              const isErr = errorLines.has(ln);
              const isFocused = highlightedLine === ln;
              return (
                <div
                  key={ln}
                  className={`${styles.codeGutterLine} ${
                    isFocused
                      ? styles.codeGutterLineFocus
                      : isErr
                        ? styles.codeGutterLineErr
                        : ""
                  }`}
                >
                  {ln}
                </div>
              );
            })}
          </div>
          <textarea
            ref={textareaRef}
            className={styles.codeTextarea}
            value={codeText}
            onChange={(e) => handleCodeChange(e.target.value)}
            onScroll={handleScrollSync}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            wrap="off"
            aria-label="Nội dung mã JSON template v2"
          />
        </div>

        <div className={styles.codeFooterMeta}>
          <span>
            {lineCount} dòng · {countAllSections(lastValidConfig)} tổng khối trên 5 trang · Mẫu:{" "}
            <code>{lastValidConfig.template_id}</code>
          </span>
          {!validation.ok && (
            <span className={styles.codeStaleNotice}>
              Xem trước bên phải giữ bản hợp lệ gần nhất cho tới khi sửa hết lỗi.
            </span>
          )}
        </div>

        {!validation.ok && validation.issues.length > 0 && (
          <div className={styles.diagnosticsBox} role="alert" aria-label="Danh sách lỗi mã template">
            <div className={styles.diagnosticsHead}>
              Chi tiết lỗi (nhấn vào từng dòng để nhảy tới vị trí chính xác)
            </div>
            <ul className={styles.diagnosticsList}>
              {validation.issues.map((issue, idx) => (
                <li key={`${issue.line}-${issue.column}-${issue.code}-${idx}`}>
                  <button
                    type="button"
                    className={styles.diagnosticBtn}
                    onClick={() => handleJumpToIssue(issue)}
                  >
                    <span className={styles.diagnosticLoc}>
                      Dòng {issue.line}, Cột {issue.column}
                    </span>
                    <span className={styles.diagnosticCode}>[{issue.code}]</span>
                    <span className={styles.diagnosticMsg}>
                      <code>{issue.path}</code>: {issue.message}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>

      {/* Right pane: Live Preview with 5-page switcher & Desktop/Mobile viewport toggle */}
      <section className={common.card} aria-label="Xem trước trực tiếp mã template">
        <div className={styles.livePreviewHeader}>
          <div>
            <h2 className={common.cardTitle}>Xem trước trực tiếp</h2>
            <p className={common.cardMeta}>
              Cập nhật tức thì khi mã JSON hợp lệ — kiểm tra đủ 5 trang trên máy tính và di động.
            </p>
          </div>
          <div className={styles.viewportGroup} role="group" aria-label="Khung xem trực tiếp">
            <button
              type="button"
              className={`${styles.viewportBtn} ${
                previewViewport === "desktop" ? styles.viewportBtnActive : ""
              }`}
              onClick={() => setPreviewViewport("desktop")}
            >
              Máy tính
            </button>
            <button
              type="button"
              className={`${styles.viewportBtn} ${
                previewViewport === "mobile" ? styles.viewportBtnActive : ""
              }`}
              onClick={() => setPreviewViewport("mobile")}
            >
              Di động 390px
            </button>
          </div>
        </div>

        <div className={styles.pageSwitcherGroup} role="tablist" aria-label="Chọn trang xem trước">
          {TEMPLATE_PAGE_TABS.map((tab) => (
            <button
              key={tab.key}
              type="button"
              role="tab"
              aria-selected={previewPage === tab.key}
              className={`${styles.pageSwitchBtn} ${
                previewPage === tab.key ? styles.pageSwitchBtnActive : ""
              }`}
              onClick={() => setPreviewPage(tab.key)}
            >
              {tab.shortLabel}
            </button>
          ))}
        </div>

        <div className={styles.livePreviewStage}>
          <div
            className={
              previewViewport === "mobile"
                ? styles.previewCanvasMobile
                : styles.previewCanvasDesktop
            }
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
          </div>
        </div>
      </section>
    </div>
  );
}
