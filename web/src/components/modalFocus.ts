"use client";

import type React from "react";
import { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import { resolveTabTrapTarget, shouldIgnoreModalEscape } from "./modalFocusUtils";

/* ---------------------------------------------------------------------------
   Modal focus stack + focus trap (shared by blocking dialogs, drawers,
   command palettes, and mobile sheets). Guarantees:
   - Only the topmost open modal intercepts Escape / Tab.
   - Listeners attach to container.ownerDocument so iframe portals never hijack parent focus.
   - Sensible initial focus immediately upon open (no missed keystrokes).
   - Focus stays trapped inside the open dialog (including Shift+Tab from container).
   - Escape yields to IME composition and expanded combobox popups.
   - Focus returns to the triggering element on dismiss (only if still in DOM
     and not dismissed by navigating to another page).
   --------------------------------------------------------------------------- */
const modalStack: symbol[] = [];

export function hasOpenModal(): boolean {
  return modalStack.length > 0;
}

const FOCUSABLE_SELECTOR = [
  'a[href]:not([tabindex="-1"])',
  'button:not([disabled]):not([tabindex="-1"])',
  'input:not([disabled]):not([type="hidden"]):not([tabindex="-1"])',
  'select:not([disabled]):not([tabindex="-1"])',
  'textarea:not([disabled]):not([tabindex="-1"])',
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

function isElementVisibleAndEnabled(el: HTMLElement): boolean {
  if (el.hasAttribute("disabled") || el.getAttribute("aria-hidden") === "true") {
    return false;
  }
  try {
    if (el.matches(":disabled")) {
      return false;
    }
  } catch {
    // Ignore unsupported selector errors in non-standard environments.
  }
  if (el.closest('[inert], [aria-hidden="true"]')) {
    return false;
  }
  const win = el.ownerDocument?.defaultView ?? window;
  const style = win.getComputedStyle(el);
  if (style.display === "none" || style.visibility === "hidden") {
    return false;
  }
  return el.getClientRects().length > 0;
}

function isVisibleFocusable(el: HTMLElement): boolean {
  if (el.tabIndex < 0) return false;
  return isElementVisibleAndEnabled(el);
}

export function getFocusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
    isVisibleFocusable
  );
}

export interface UseModalFocusTrapOptions {
  open: boolean;
  containerRef: React.RefObject<HTMLElement | null>;
  initialFocusRef?: React.RefObject<HTMLElement | null>;
  resolveInitialFocus?: (container: HTMLElement) => HTMLElement | null;
  onEscape?: () => void;
  disableEscape?: boolean;
  shouldRestoreFocusRef?: React.RefObject<boolean>;
  /** Fallback trigger element when the opener was blurred before open (e.g. disabled during an async call). */
  returnFocusToRef?: React.RefObject<HTMLElement | null>;
}

export function useModalFocusTrap({
  open,
  containerRef,
  initialFocusRef,
  resolveInitialFocus,
  onEscape,
  disableEscape = false,
  shouldRestoreFocusRef,
  returnFocusToRef,
}: UseModalFocusTrapOptions): void {
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const openPathRef = useRef<string | null>(null);
  const wasOpenRef = useRef(false);
  const onEscapeRef = useRef(onEscape);
  const disableEscapeRef = useRef(disableEscape);
  const resolveInitialFocusRef = useRef(resolveInitialFocus);
  const restoreFlagRef = useRef(shouldRestoreFocusRef);
  const returnFocusFallbackRef = useRef(returnFocusToRef);

  onEscapeRef.current = onEscape;
  disableEscapeRef.current = disableEscape;
  resolveInitialFocusRef.current = resolveInitialFocus;
  restoreFlagRef.current = shouldRestoreFocusRef;
  returnFocusFallbackRef.current = returnFocusToRef;

  const restoreFocusIfEligible = useCallback(() => {
    const flagRef = restoreFlagRef.current;
    const shouldRestore = flagRef ? flagRef.current : true;
    const prev = previousFocusRef.current;
    const openedPath = openPathRef.current;
    previousFocusRef.current = null;
    openPathRef.current = null;

    if (!shouldRestore || !prev || !prev.isConnected || typeof prev.focus !== "function") {
      return;
    }
    const win = prev.ownerDocument?.defaultView ?? (typeof window !== "undefined" ? window : null);
    if (win && openedPath !== null && win.location.pathname !== openedPath) {
      return;
    }
    prev.focus({ preventScroll: true });
  }, []);

  const focusInside = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;

    const explicit = initialFocusRef?.current;
    if (explicit && container.contains(explicit) && isElementVisibleAndEnabled(explicit)) {
      explicit.focus({ preventScroll: true });
      return;
    }

    const custom = resolveInitialFocusRef.current?.(container);
    if (custom && container.contains(custom) && isElementVisibleAndEnabled(custom)) {
      custom.focus({ preventScroll: true });
      return;
    }

    const autoFocused = container.querySelector<HTMLElement>("[data-autofocus]");
    if (autoFocused && isElementVisibleAndEnabled(autoFocused)) {
      autoFocused.focus({ preventScroll: true });
      return;
    }

    const focusables = getFocusableElements(container);
    if (focusables.length > 0) {
      focusables[0].focus({ preventScroll: true });
      return;
    }

    container.focus({ preventScroll: true });
  }, [containerRef, initialFocusRef]);

  useLayoutEffect(() => {
    if (!open) {
      if (wasOpenRef.current) {
        wasOpenRef.current = false;
        restoreFocusIfEligible();
      }
      return;
    }

    const container = containerRef.current;
    if (!container) return;

    const doc = container.ownerDocument;
    const win = doc.defaultView ?? window;

    if (!wasOpenRef.current) {
      const active = doc.activeElement;
      const fallback = returnFocusFallbackRef.current?.current ?? null;
      const isFocusableActive =
        active !== null &&
        active !== doc.body &&
        typeof (active as HTMLElement).focus === "function" &&
        !container.contains(active);
      previousFocusRef.current = isFocusableActive
        ? (active as HTMLElement)
        : fallback && !container.contains(fallback)
          ? fallback
          : null;
      openPathRef.current = win.location?.pathname ?? null;
      wasOpenRef.current = true;
    }

    const token = Symbol("modal-focus-trap");
    modalStack.push(token);

    focusInside();
    const raf = win.requestAnimationFrame(() => {
      const cur = containerRef.current;
      if (!cur) return;
      if (modalStack[modalStack.length - 1] !== token) return;
      if (!cur.contains(doc.activeElement)) {
        focusInside();
      }
    });

    const isTop = () => modalStack[modalStack.length - 1] === token;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (!isTop()) return;
      const cur = containerRef.current;
      if (!cur) return;

      if (e.key === "Escape") {
        if (shouldIgnoreModalEscape(e, doc.activeElement)) {
          return;
        }
        e.preventDefault();
        e.stopPropagation();
        if (!disableEscapeRef.current) {
          onEscapeRef.current?.();
        }
        return;
      }

      if (e.key === "Tab") {
        const focusables = getFocusableElements(cur);
        const active = doc.activeElement;
        const target = resolveTabTrapTarget<Element>({
          shiftKey: e.shiftKey,
          container: cur,
          active,
          containerContainsActive: active !== null && cur.contains(active),
          focusables,
        });
        if (target && typeof (target as HTMLElement).focus === "function") {
          e.preventDefault();
          (target as HTMLElement).focus({ preventScroll: true });
        }
      }
    };

    const handleFocusIn = (e: FocusEvent) => {
      if (!isTop()) return;
      const cur = containerRef.current;
      if (!cur) return;
      const target = e.target;
      if (
        target &&
        typeof (target as Node).nodeType === "number" &&
        !cur.contains(target as Node)
      ) {
        focusInside();
      }
    };

    doc.addEventListener("keydown", handleKeyDown, true);
    doc.addEventListener("focusin", handleFocusIn);

    return () => {
      win.cancelAnimationFrame(raf);
      doc.removeEventListener("keydown", handleKeyDown, true);
      doc.removeEventListener("focusin", handleFocusIn);
      const idx = modalStack.lastIndexOf(token);
      if (idx >= 0) modalStack.splice(idx, 1);
    };
  }, [open, containerRef, focusInside, restoreFocusIfEligible]);

  useEffect(() => {
    return () => {
      if (wasOpenRef.current) {
        wasOpenRef.current = false;
        restoreFocusIfEligible();
      }
    };
  }, [restoreFocusIfEligible]);
}
