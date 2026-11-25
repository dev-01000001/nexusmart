"use client";

import { useEffect } from "react";
import { useSearchParams } from "next/navigation";

export type DemoViewState = "loading" | "empty" | "error" | null;

/**
 * Reads optional `?state=loading|empty|error` query parameter for QA and state previews.
 */
export function usePageStateParam(): DemoViewState {
  const params = useSearchParams();
  const raw = params.get("state");
  if (raw === "loading" || raw === "empty" || raw === "error") {
    return raw;
  }
  return null;
}

type RevealWindow = Pick<Window, "innerHeight" | "matchMedia"> & {
  IntersectionObserver?: typeof IntersectionObserver;
  MutationObserver?: typeof MutationObserver;
};

/**
 * Observes existing and dynamically inserted `[data-reveal]` elements and reveals them
 * as they enter the viewport (or immediately when reduced motion is preferred or
 * `IntersectionObserver` is unavailable). Also restores `.is-in` if React re-renders
 * `className` on an already-revealed element.
 */
export function observeRevealElements(
  rootDocument: Document = document,
  win: RevealWindow = window as RevealWindow
): () => void {
  if (typeof rootDocument === "undefined" || typeof win === "undefined") {
    return () => {};
  }

  const prefersReduced = Boolean(
    win.matchMedia && win.matchMedia("(prefers-reduced-motion: reduce)").matches
  );
  const IntersectionObsCtor = win.IntersectionObserver;
  const immediate = prefersReduced || typeof IntersectionObsCtor === "undefined";

  const revealed = new WeakSet<Element>();
  const observed = new WeakSet<Element>();

  const intersectionObserver = immediate
    ? null
    : new IntersectionObsCtor(
        (entries, obs) => {
          for (const entry of entries) {
            if (entry.isIntersecting) {
              revealed.add(entry.target);
              entry.target.classList.add("is-in");
              observed.delete(entry.target);
              obs.unobserve(entry.target);
            }
          }
        },
        { rootMargin: "0px 0px -24px 0px", threshold: 0.04 }
      );

  const registerNode = (node: Element) => {
    if (!node.hasAttribute("data-reveal")) return;
    if (node.classList.contains("is-in")) {
      revealed.add(node);
      return;
    }
    if (immediate || revealed.has(node)) {
      revealed.add(node);
      node.classList.add("is-in");
      return;
    }
    if (observed.has(node)) return;

    const rect = node.getBoundingClientRect();
    if (rect.top < win.innerHeight * 0.96) {
      revealed.add(node);
      node.classList.add("is-in");
    } else if (intersectionObserver) {
      observed.add(node);
      intersectionObserver.observe(node);
    }
  };

  const scanSubtree = (root: ParentNode | Element) => {
    if ("hasAttribute" in root && root.hasAttribute("data-reveal")) {
      registerNode(root);
    }
    if ("querySelectorAll" in root) {
      const descendants = root.querySelectorAll("[data-reveal]");
      for (let i = 0; i < descendants.length; i += 1) {
        registerNode(descendants[i]);
      }
    }
  };

  scanSubtree(rootDocument);

  const observeTarget = rootDocument.body ?? rootDocument.documentElement;
  const MutationObsCtor = win.MutationObserver;
  const mutationObserver =
    observeTarget && typeof MutationObsCtor !== "undefined"
      ? new MutationObsCtor((mutations) => {
          for (const mutation of mutations) {
            if (mutation.type === "childList") {
              for (let i = 0; i < mutation.addedNodes.length; i += 1) {
                const added = mutation.addedNodes[i];
                if (added.nodeType === 1) {
                  scanSubtree(added as Element);
                }
              }
            } else if (mutation.type === "attributes" && mutation.target.nodeType === 1) {
              const target = mutation.target as Element;
              if (target.hasAttribute("data-reveal") && !target.classList.contains("is-in")) {
                registerNode(target);
              }
            }
          }
        })
      : null;

  mutationObserver?.observe(observeTarget, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["class", "data-reveal"],
  });

  return () => {
    mutationObserver?.disconnect();
    intersectionObserver?.disconnect();
  };
}

/**
 * Progressively reveals `[data-reveal]` elements as they enter the viewport.
 * Falls back immediately if IntersectionObserver is unavailable or reduced motion is preferred.
 */
export function useRevealOnScroll(depKey?: string | number) {
  useEffect(() => observeRevealElements(document, window as RevealWindow), [depKey]);
}
