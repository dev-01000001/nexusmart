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
import { SitePreviewFrame, type DevicePreviewWidth } from "./SitePreviewFrame";
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
    <div className={styles.codeWorkspace}>
      {/* Left pane: Line-aware JSON editor + strict schema diagnostics */}
      <section className={common.card} aria-label="Trình sửa mã template JSON v2">
        <div className={styles.codeHeaderRow}>
          <h2 className={common.cardTitle}>Mã JSON v2</h2>
          <span
            className={`${common.pill} ${overByteLimit ? common.pillNeg : ""}`}
            aria-label="Dung lượng cấu hình"
          >
            {formatBytesVi(byteCount)} / {formatBytesVi(MAX_CONFIG_BYTES)} B
          </span>
        </div>

        <div className={styles.codeToolbar}>
          <div className={styles.codeToolbarActions}>
            <Button variant="outline" size="sm" shape="app" onClick={handleBeautify}>
              Định dạng
            </Button>
            <Button variant="outline" size="sm" shape="app" onClick={handleSyncFromVisual}>
              Đồng bộ
            </Button>
            <Button variant="outline" size="sm" shape="app" onClick={handleResetToBaseTemplate}>
              Đặt lại mẫu gốc
            </Button>
          </div>
          <div className={styles.codeStatusWrap} aria-live="polite">
            {validation.ok ? (
              <span className={`${common.pill} ${common.pillPos}`}>Hợp lệ, WCAG đạt</span>
            ) : (
              <span className={`${common.pill} ${common.pillNeg}`}>
                {validation.issues.length} lỗi cần sửa
              </span>
            )}
          </div>
        </div>

        <div className={styles.codeEditorShell}>
          <div ref={gutterRef} className={styles.codeGutter} aria-hidden="true">
            {Array.from({ length: lineCount }, (_, i) => {
              const ln = i + 1;
              const isErr = errorLines.has(ln);
              const isFocused = highlightedLine === ln;
              return (
                <div
                  key={ln}
                  className={`${styles.codeGutterLine} ${
                    isFocused ? styles.codeGutterLineFocus : isErr ? styles.codeGutterLineErr : ""
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
            {lineCount} dòng, {countAllSections(lastValidConfig)} khối,{" "}
            <code>{lastValidConfig.template_id}</code>
          </span>
          {!validation.ok && (
            <span className={styles.codeStaleNotice}>Giữ bản xem trước hợp lệ gần nhất.</span>
          )}
        </div>

        {!validation.ok && validation.issues.length > 0 && (
          <div
            className={styles.diagnosticsBox}
            role="alert"
            aria-label="Danh sách lỗi mã template"
          >
            <div className={styles.diagnosticsHead}>Nhấn vào lỗi để nhảy tới vị trí chính xác</div>
            <ul className={styles.diagnosticsList}>
              {validation.issues.map((issue, idx) => (
                <li key={`${issue.line}-${issue.column}-${issue.code}-${idx}`}>
                  <button
                    type="button"
                    className={styles.diagnosticBtn}
                    onClick={() => handleJumpToIssue(issue)}
                  >
                    <span className={styles.diagnosticLoc}>
                      Dòng {issue.line}:{issue.column}
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

      {/* Right pane (>= 60%): Live Preview with 5-page switcher & 390 / 768 / 1440 device frame */}
      <section className={styles.previewPaneSticky} aria-label="Xem trước trực tiếp mã template">
        <div className={styles.previewPaneBar}>
          <div
            className={styles.pageSwitcherGroup}
            role="tablist"
            aria-label="Chọn trang xem trước"
          >
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

          <div className={styles.viewportGroup} role="group" aria-label="Khung xem trực tiếp">
            {(["390", "768", "1440"] as const).map((w) => (
              <button
                key={w}
                type="button"
                aria-pressed={previewDevice === w}
                className={`${styles.viewportBtn} ${
                  previewDevice === w ? styles.viewportBtnActive : ""
                }`}
                onClick={() => setPreviewDevice(w)}
              >
                {w}
              </button>
            ))}
          </div>
        </div>

        <div className={styles.previewStage}>
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
      </section>
    </div>
  );
}
