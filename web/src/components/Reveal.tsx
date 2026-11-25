"use client";

import { useEffect } from "react";
import {
  REVEAL_SELECTOR,
  easeOutCubic,
  formatCountMetric,
  isRectImmediatelyVisible,
} from "./revealUtils";

const MAX_HYDRATE_FRAMES = 60;

// Defer DOM attribute writes until React claims the node so streamed Suspense children never hit hydration mismatches.
function isReactClaimed(el: HTMLElement): boolean {
  for (const key of Object.keys(el)) {
    if (key.startsWith("__reactFiber$") || key.startsWith("__reactProps$")) {
      return true;
    }
  }
  return false;
}

function runCountOnce(
  container: HTMLElement,
  reduced: boolean,
  rafIds: Set<number>,
  isCancelled: () => boolean
): void {
  const targets: HTMLElement[] = container.hasAttribute("data-count-to")
    ? [container]
    : Array.from(container.querySelectorAll<HTMLElement>("[data-count-to]"));

  for (const el of targets) {
    if (el.dataset.counted === "1") continue;
    const raw = Number(el.dataset.countTo);
    if (!Number.isFinite(raw)) continue;

    el.dataset.counted = "1";
    // Under reduced motion (or without RAF), keep SSR/React text intact instead of rewriting it.
    if (reduced || typeof requestAnimationFrame === "undefined") {
      continue;
    }

    const opts = {
      format: el.getAttribute("data-count-format"),
      decimals: Math.max(0, Number.parseInt(el.dataset.countDecimals ?? "0", 10) || 0),
      prefix: el.dataset.countPrefix ?? "",
      suffix: el.dataset.countSuffix ?? "",
    };

    const duration = 800;
    const start = performance.now();
    const step = (now: number) => {
      if (isCancelled() || !el.isConnected || Number(el.dataset.countTo) !== raw) {
        return;
      }
      const progress = Math.min(1, (now - start) / duration);
      el.textContent = formatCountMetric(raw * easeOutCubic(progress), opts);
      if (progress < 1) {
        const nextId = requestAnimationFrame((ts) => {
          rafIds.delete(nextId);
          step(ts);
        });
        rafIds.add(nextId);
      } else {
        el.textContent = formatCountMetric(raw, opts);
      }
    };
    const firstId = requestAnimationFrame((ts) => {
      rafIds.delete(firstId);
      step(ts);
    });
    rafIds.add(firstId);
  }
}

let activeObservers = 0;

/**
 * Single root scroll-reveal engine mounted once in `web/src/app/layout.tsx`.
 * Uses one IntersectionObserver (`threshold: 0`) + one MutationObserver (`childList`, `subtree`, `data-reveal`)
 * and writes `data-shown` only after React has claimed the node.
 */
export function RevealObserver() {
  useEffect(() => {
    if (typeof window === "undefined" || typeof document === "undefined") return;
    (window as Window & { __nmRevealReady?: boolean }).__nmRevealReady = true;
    if (activeObservers > 0) return;
    activeObservers += 1;

    let cancelled = false;
    const isCancelled = () => cancelled;
    const reduced =
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    const observed = new WeakSet<HTMLElement>();
    const scheduled = new WeakSet<HTMLElement>();
    const rafIds = new Set<number>();

    const commitShow = (el: HTMLElement) => {
      if (!el.hasAttribute("data-shown")) {
        el.setAttribute("data-shown", "");
      }
      if (el.getAttribute("data-reveal") === "count") {
        runCountOnce(el, reduced, rafIds, isCancelled);
      }
    };

    const showElement = (el: HTMLElement) => {
      if (el.hasAttribute("data-shown") || scheduled.has(el)) return;
      if (isReactClaimed(el) || typeof requestAnimationFrame === "undefined") {
        commitShow(el);
        return;
      }

      scheduled.add(el);
      let frames = 0;
      const waitForHydration = () => {
        if (cancelled || !el.isConnected || el.hasAttribute("data-shown")) return;
        frames += 1;
        if (isReactClaimed(el) || frames >= MAX_HYDRATE_FRAMES) {
          commitShow(el);
          return;
        }
        const id = requestAnimationFrame(() => {
          rafIds.delete(id);
          waitForHydration();
        });
        rafIds.add(id);
      };

      const firstId = requestAnimationFrame(() => {
        rafIds.delete(firstId);
        waitForHydration();
      });
      rafIds.add(firstId);
    };

    const io =
      !reduced && typeof IntersectionObserver !== "undefined"
        ? new IntersectionObserver(
            (entries) => {
              for (const entry of entries) {
                if (entry.isIntersecting) {
                  const target = entry.target as HTMLElement;
                  showElement(target);
                  io?.unobserve(target);
                }
              }
            },
            { rootMargin: "0px", threshold: 0 }
          )
        : null;

    const register = (el: HTMLElement) => {
      if (el.hasAttribute("data-shown") || scheduled.has(el)) return;
      if (reduced || !io) {
        showElement(el);
        return;
      }

      const vh = window.innerHeight || 900;
      const rect = el.getBoundingClientRect();
      if (isRectImmediatelyVisible(rect, vh)) {
        showElement(el);
        return;
      }

      if (observed.has(el)) return;
      observed.add(el);
      io.observe(el);
    };

    const scanNode = (root: Node) => {
      if (!(root instanceof Element)) return;
      if (root.matches(REVEAL_SELECTOR)) {
        register(root as HTMLElement);
      }
      const descendants = root.querySelectorAll<HTMLElement>(REVEAL_SELECTOR);
      for (const el of descendants) {
        register(el);
      }
    };

    if (document.body) {
      scanNode(document.body);
    }

    const mo =
      typeof MutationObserver !== "undefined" && document.body
        ? new MutationObserver((mutations) => {
            for (const m of mutations) {
              if (m.type === "attributes") {
                if (m.target instanceof HTMLElement && m.target.matches(REVEAL_SELECTOR)) {
                  register(m.target);
                }
                continue;
              }
              for (const node of m.addedNodes) {
                scanNode(node);
              }
            }
          })
        : null;

    if (mo && document.body) {
      mo.observe(document.body, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ["data-reveal"],
      });
    }

    return () => {
      cancelled = true;
      for (const id of rafIds) cancelAnimationFrame(id);
      rafIds.clear();
      activeObservers = Math.max(0, activeObservers - 1);
      io?.disconnect();
      mo?.disconnect();
    };
  }, []);

  return null;
}
