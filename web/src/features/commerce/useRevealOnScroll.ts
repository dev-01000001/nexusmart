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

/**
 * Progressively reveals `[data-reveal]` elements as they enter the viewport.
 * Falls back immediately if IntersectionObserver is unavailable or reduced motion is preferred.
 */
export function useRevealOnScroll(depKey?: string | number) {
  useEffect(() => {
    const nodes = Array.from(document.querySelectorAll<HTMLElement>("[data-reveal]"));
    if (nodes.length === 0) return;

    const prefersReduced =
      typeof window !== "undefined" &&
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    if (prefersReduced || typeof IntersectionObserver === "undefined") {
      nodes.forEach((n) => n.classList.add("is-in"));
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add("is-in");
            observer.unobserve(entry.target);
          }
        });
      },
      { rootMargin: "0px 0px -24px 0px", threshold: 0.04 }
    );

    nodes.forEach((node) => {
      const rect = node.getBoundingClientRect();
      if (rect.top < window.innerHeight * 0.96) {
        node.classList.add("is-in");
      } else {
        observer.observe(node);
      }
    });

    return () => observer.disconnect();
  }, [depKey]);
}
