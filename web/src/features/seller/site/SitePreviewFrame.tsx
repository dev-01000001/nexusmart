"use client";

import React, { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/utils";
import { stripPreviewJsClass } from "./siteEditorUtils";

export type DevicePreviewWidth = "1440" | "768" | "390";

export interface SitePreviewFrameProps {
  device: DevicePreviewWidth;
  title: string;
  hostLabel?: string;
  showChromeBar?: boolean;
  resetScrollKey?: string;
  viewportHeight?: number;
  onEscape?: () => void;
  children: React.ReactNode;
}

const IFRAME_SRC_DOC =
  '<!DOCTYPE html><html lang="vi"><head><meta charset="utf-8" /><meta name="viewport" content="width=device-width, initial-scale=1" /></head><body><div id="nm-preview-root"></div></body></html>';

const BASE_PREVIEW_CSS = `
html, body {
  margin: 0;
  padding: 0;
  overflow-x: hidden;
  background: var(--ui-card, #ffffff);
}
#nm-preview-root {
  min-height: 100%;
}
[data-reveal],
[data-reveal="rows"] > *,
[data-reveal="lines"] [data-line],
[data-reveal="clip"] > img,
[data-reveal="clip"] > video,
[data-rise],
.lines [data-line] {
  opacity: 1 !important;
  transform: none !important;
  clip-path: none !important;
  animation: none !important;
  transition: none !important;
}
@media (max-width: 1024px) {
  [data-site-preview="true"] header[data-site-header] nav[aria-label="Điều hướng cửa hàng"] {
    display: none !important;
  }
}
`;

function syncMirroredAttribute(from: Element, to: Element, attr: string): void {
  const val = from.getAttribute(attr);
  if (val === null) to.removeAttribute(attr);
  else to.setAttribute(attr, val);
}

function isMirrorableStyleNode(node: Node): node is HTMLStyleElement | HTMLLinkElement {
  if (node.nodeType !== Node.ELEMENT_NODE) return false;
  const el = node as Element;
  if (el.tagName === "STYLE") return true;
  if (el.tagName === "LINK") {
    const rel = (el.getAttribute("rel") || "").toLowerCase();
    return rel === "stylesheet";
  }
  return false;
}

/**
 * Isolated same-origin iframe preview using React createPortal.
 * Mirrors parent stylesheets (including Next dev HMR style mutations) so storefront
 * media queries evaluate strictly against the selected device width (390 / 768 / 1440)
 * while reflecting unsaved draft edits live and blocking link/form navigation.
 */
export function SitePreviewFrame({
  device,
  title,
  hostLabel,
  showChromeBar = false,
  resetScrollKey,
  viewportHeight = 580,
  onEscape,
  children,
}: SitePreviewFrameProps) {
  const stageRef = useRef<HTMLDivElement | null>(null);
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const onEscapeRef = useRef(onEscape);
  onEscapeRef.current = onEscape;
  const [stageWidth, setStageWidth] = useState<number>(680);
  const [mountNode, setMountNode] = useState<HTMLElement | null>(null);

  // Track available preview stage width for responsive down-scaling when stage < targetWidth
  useEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const measure = () => {
      const w = Math.floor(el.clientWidth);
      if (w > 0) setStageWidth(w);
    };
    measure();
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(measure);
      ro.observe(el);
      return () => ro.disconnect();
    }
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, []);

  const syncIframeDocument = useCallback(() => {
    const iframe = iframeRef.current;
    const doc = iframe?.contentDocument;
    if (!doc || !doc.head || !doc.body) return;
    const root = doc.getElementById("nm-preview-root");
    if (!root) return;

    // Copy font variable classes and inline tokens from parent html/body without `js`
    doc.documentElement.className = stripPreviewJsClass(document.documentElement.className);
    if (document.documentElement.style.cssText) {
      doc.documentElement.style.cssText = document.documentElement.style.cssText;
    }
    doc.body.className = document.body.className;

    setMountNode((prev) => (prev === root ? prev : root));
  }, []);

  useEffect(() => {
    const iframe = iframeRef.current;
    if (!iframe) return;
    if (iframe.contentDocument?.readyState === "complete") {
      syncIframeDocument();
    }
  }, [syncIframeDocument]);

  // Mirror stylesheets + attach link/form/Escape interceptors once mountNode is ready
  useEffect(() => {
    if (!mountNode) return;
    const doc = mountNode.ownerDocument;
    if (!doc || !doc.head) return;

    const mirrored = new Map<Node, HTMLElement>();

    // Ensure base preview stylesheet exists (keeps [data-reveal] visible inside iframe)
    let baseStyle = doc.head.querySelector<HTMLStyleElement>('style[data-nm-preview-base="1"]');
    if (!baseStyle) {
      baseStyle = doc.createElement("style");
      baseStyle.setAttribute("data-nm-preview-base", "1");
      baseStyle.textContent = BASE_PREVIEW_CSS;
      doc.head.appendChild(baseStyle);
    }

    const addMirror = (src: HTMLStyleElement | HTMLLinkElement) => {
      if (mirrored.has(src)) return;
      const clone = src.cloneNode(true) as HTMLElement;
      doc.head.insertBefore(clone, baseStyle);
      mirrored.set(src, clone);
    };

    const removeMirror = (src: Node) => {
      const existing = mirrored.get(src);
      if (existing) {
        existing.remove();
        mirrored.delete(src);
      }
    };

    const updateMirror = (src: Node) => {
      const targetEl = src.nodeType === Node.TEXT_NODE ? src.parentNode : src;
      if (!targetEl || !isMirrorableStyleNode(targetEl)) return;
      const existing = mirrored.get(targetEl);
      if (!existing) {
        addMirror(targetEl);
        return;
      }
      if (targetEl.tagName === "STYLE") {
        existing.textContent = targetEl.textContent;
        syncMirroredAttribute(targetEl, existing, "media");
        (existing as HTMLStyleElement).disabled = (targetEl as HTMLStyleElement).disabled;
      } else if (targetEl.tagName === "LINK") {
        syncMirroredAttribute(targetEl, existing, "href");
        syncMirroredAttribute(targetEl, existing, "media");
        (existing as HTMLLinkElement).disabled = (targetEl as HTMLLinkElement).disabled;
      }
    };

    // Initial copy of all parent head stylesheets
    for (const child of Array.from(document.head.childNodes)) {
      if (isMirrorableStyleNode(child)) {
        addMirror(child);
      }
    }

    // Watch parent head for dev HMR / lazy CSS chunks
    const headObserver = new MutationObserver((mutations) => {
      for (const m of mutations) {
        if (m.type === "childList") {
          for (const added of Array.from(m.addedNodes)) {
            if (isMirrorableStyleNode(added)) {
              addMirror(added);
            } else if (added.nodeType === Node.TEXT_NODE) {
              updateMirror(added);
            }
          }
          for (const removed of Array.from(m.removedNodes)) {
            if (removed.nodeType === Node.TEXT_NODE) {
              updateMirror(m.target);
            } else {
              removeMirror(removed);
            }
          }
        } else if (m.type === "characterData" || m.type === "attributes") {
          updateMirror(m.target);
        }
      }
    });

    headObserver.observe(document.head, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["href", "media", "disabled"],
    });

    // Block all <a> link navigation (left + middle click) and <form> submissions inside the preview iframe
    const onCaptureClick = (ev: MouseEvent) => {
      const target = ev.target;
      const iframeElement = doc.defaultView?.Element;
      const isEl =
        target instanceof Element || (iframeElement != null && target instanceof iframeElement);
      if (!isEl) return;
      const anchor = target.closest("a[href]");
      if (anchor) {
        ev.preventDefault();
        ev.stopPropagation();
      }
    };

    const onCaptureSubmit = (ev: Event) => {
      ev.preventDefault();
      ev.stopPropagation();
    };

    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !ev.isComposing && onEscapeRef.current) {
        ev.preventDefault();
        onEscapeRef.current();
      }
    };

    doc.addEventListener("click", onCaptureClick, true);
    doc.addEventListener("auxclick", onCaptureClick, true);
    doc.addEventListener("submit", onCaptureSubmit, true);
    doc.addEventListener("keydown", onKeyDown);

    return () => {
      headObserver.disconnect();
      doc.removeEventListener("click", onCaptureClick, true);
      doc.removeEventListener("auxclick", onCaptureClick, true);
      doc.removeEventListener("submit", onCaptureSubmit, true);
      doc.removeEventListener("keydown", onKeyDown);
      for (const clone of mirrored.values()) {
        clone.remove();
      }
      mirrored.clear();
    };
  }, [mountNode]);

  // Reset scroll to top when switching preview page or template
  useEffect(() => {
    if (!resetScrollKey) return;
    iframeRef.current?.contentWindow?.scrollTo(0, 0);
  }, [resetScrollKey]);

  const targetWidth = Number(device);
  const scale = stageWidth > 0 ? Math.min(1, stageWidth / targetWidth) : 1;
  const visualWidth =
    stageWidth > 0 ? Math.min(stageWidth, Math.max(200, Math.floor(targetWidth * scale))) : 360;
  const scaledIframeHeight = Math.ceil(viewportHeight / scale);

  const frameBorderClass =
    device === "390"
      ? "rounded-[14px] border-input"
      : device === "768"
        ? "rounded-[10px] border-input"
        : "rounded-lg border-border";

  return (
    <div ref={stageRef} className="flex w-full min-w-0 justify-center">
      <div
        className={cn(
          "mx-auto max-w-full overflow-hidden border bg-card transition-[width,border-radius] duration-200 ease-out",
          frameBorderClass
        )}
        style={{ width: `${visualWidth}px` }}
        data-preview-device={device}
      >
        {showChromeBar && (
          <div className="flex h-8 items-center justify-between gap-2 border-b border-border bg-muted/60 px-3 text-xs text-muted-foreground">
            <span className="inline-flex shrink-0 items-center gap-1.5" aria-hidden="true">
              <span className="size-2 rounded-full bg-input/60" />
              <span className="size-2 rounded-full bg-input/60" />
              <span className="size-2 rounded-full bg-input/60" />
            </span>
            <span className="min-w-0 truncate text-xs text-foreground/80">{hostLabel || ""}</span>
            <span className="shrink-0 text-xs whitespace-nowrap tabular-nums text-muted-foreground">
              {device}px
            </span>
          </div>
        )}

        <div
          className="relative w-full overflow-hidden bg-card"
          style={{ height: `${viewportHeight}px` }}
        >
          <iframe
            ref={iframeRef}
            title={title}
            srcDoc={IFRAME_SRC_DOC}
            onLoad={syncIframeDocument}
            className="block origin-top-left border-0 bg-card"
            style={{
              width: `${targetWidth}px`,
              height: `${scaledIframeHeight}px`,
              transform: scale < 1 ? `scale(${scale})` : undefined,
            }}
          />
        </div>
      </div>

      {mountNode ? createPortal(children, mountNode) : null}
    </div>
  );
}
